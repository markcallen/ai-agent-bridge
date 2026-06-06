package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/internal/localserver"
)

// detachKey is ctrl-] (0x1d), used to detach from a session without stopping it.
const detachKey = 0x1d

func newRunCmd() *cobra.Command {
	var (
		providerName string
		project      string
		timeout      time.Duration
		noTTY        bool
	)

	cmd := &cobra.Command{
		Use:   "run [directory]",
		Short: "Start an AI agent session in a directory",
		Long: `Start a local bridge server (if not already running), create a new
session with the specified provider, and attach your terminal.

If another instance is already running, the existing server is reused.

Press ctrl-] to detach from the session without stopping it.
Use 'bridgectl session attach <id>' to reattach later.

Use --no-tty to run without a terminal, reading from stdin and writing to
stdout. Useful for scripting, piping input, and automated tests.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}
			absDir, err := filepath.Abs(dir)
			if err != nil {
				return fmt.Errorf("resolve directory: %w", err)
			}
			if _, err := os.Stat(absDir); err != nil {
				return fmt.Errorf("directory %q: %w", absDir, err)
			}
			if noTTY {
				return runSessionNoTTY(absDir, providerName, project, timeout)
			}
			return runSession(absDir, providerName, project, timeout)
		},
	}

	cmd.Flags().StringVarP(&providerName, "provider", "p", "claude", "AI provider (claude, codex, opencode, gemini, echo)")
	cmd.Flags().StringVar(&project, "project", "local", "project ID")
	cmd.Flags().DurationVarP(&timeout, "timeout", "t", 30*time.Minute, "session timeout")
	cmd.Flags().BoolVar(&noTTY, "no-tty", false, "run without a terminal (for scripting and tests)")

	return cmd
}

func runSession(dir, providerName, project string, timeout time.Duration) error {
	// Validate terminal before starting a session to avoid orphaning a
	// provider process when stdin is not interactive.
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("stdin is not a terminal")
	}

	// Ensure a server is running (spawns a local-mode background process if needed).
	if err := ensureServer(); err != nil {
		return err
	}

	// Connect using auto-detected mode (local or secure).
	client, err := connectClient("", timeout)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	client.SetProject(project)

	cols, rows := currentTTYSize()
	sessionID := uuid.NewString()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if _, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   project,
		SessionId:   sessionID,
		RepoPath:    dir,
		Provider:    providerName,
		InitialCols: cols,
		InitialRows: rows,
	}); err != nil {
		return fmt.Errorf("start session: %w", err)
	}

	// Put terminal in raw mode.
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("set raw terminal: %w", err)
	}
	var restoreOnce sync.Once
	restore := func() {
		restoreOnce.Do(func() {
			_ = term.Restore(fd, oldState)
		})
	}
	defer restore()

	stream, err := client.AttachSession(ctx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  uuid.NewString(),
		AfterSeq:  0,
	})
	if err != nil {
		restore()
		return fmt.Errorf("attach session: %w", err)
	}

	var detached atomic.Bool

	// Handle signals.
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// SIGWINCH handling (resize) — only on unix.
	setupSigwinch(sigCh)

	go func() {
		for sig := range sigCh {
			if isSigwinch(sig) {
				c, r := currentTTYSize()
				_, _ = client.ResizeSession(context.Background(), &bridgev1.ResizeSessionRequest{
					SessionId: sessionID,
					ClientId:  stream.ClientID(),
					Cols:      c,
					Rows:      r,
				})
				continue
			}
			// SIGINT/SIGTERM → stop session and let RecvAll unwind.
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
			_, _ = client.StopSession(stopCtx, &bridgev1.StopSessionRequest{
				SessionId: sessionID,
				Force:     true,
			})
			stopCancel()
			cancel()
			return
		}
	}()

	// Forward stdin → session, watching for detach key.
	go func() {
		buf := make([]byte, 1024)
		for {
			n, readErr := os.Stdin.Read(buf)
			if n > 0 {
				// Check for detach key (ctrl-]).
				for i := 0; i < n; i++ {
					if buf[i] == detachKey {
						detached.Store(true)
						cancel()
						return
					}
				}
				data := normalizeTTYInput(buf[:n])
				_, _ = client.WriteInput(context.Background(), &bridgev1.WriteInputRequest{
					SessionId: sessionID,
					ClientId:  stream.ClientID(),
					Data:      data,
				})
			}
			if readErr != nil {
				if readErr != io.EOF {
					fmt.Fprintf(os.Stderr, "\r\nstdin read failed: %v\r\n", readErr)
				}
				return
			}
		}
	}()

	// Receive session output → stdout.
	err = stream.RecvAll(ctx, func(ev *bridgev1.AttachSessionEvent) error {
		switch ev.Type {
		case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_OUTPUT:
			_, writeErr := os.Stdout.Write(ev.Payload)
			return writeErr
		case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_REPLAY_GAP:
			_, writeErr := fmt.Fprintf(os.Stderr, "\r\n[ai-agent-bridge] replay gap: oldest=%d last=%d\r\n", ev.OldestSeq, ev.LastSeq)
			return writeErr
		case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ERROR:
			return errors.New(ev.Error)
		default:
			return nil
		}
	})
	restore()

	if detached.Load() {
		fmt.Fprintf(os.Stderr, "\r\nDetached from session %s\r\n", sessionID)
		fmt.Fprintf(os.Stderr, "Reattach with: bridgectl session attach %s\r\n", sessionID)
	} else if err != nil {
		fmt.Fprintf(os.Stderr, "\r\nsession ended: %v\r\n", err)
	}
	return nil
}

// ensureServer ensures a bridge server is running. If none is found, it spawns
// "bridgectl server start" as a background process in local mode and
// waits for it to become healthy. For secure mode, the user must start the
// server explicitly with --listen.
func ensureServer() error {
	// Check for existing server (local or secure).
	target, _ := localserver.DiscoverTarget("")
	if target != "" {
		return nil
	}

	// Find our own binary to spawn the server (local mode only).
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	cmd := exec.Command(self, "server", "start")
	setDetachedProcess(cmd)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start server process: %w", err)
	}
	// Detach — don't wait for the child.
	go func() { _ = cmd.Wait() }()

	// Poll until the server is healthy.
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		target, _ = localserver.DiscoverTarget("")
		if target != "" {
			return nil
		}
	}
	return fmt.Errorf("server did not start within 5s")
}

// runSessionNoTTY runs a session without a terminal, forwarding raw stdin to
// the provider and writing output to stdout. Used for scripting, piping, and
// automated tests (e.g. the echo provider in CI).
func runSessionNoTTY(dir, providerName, project string, timeout time.Duration) error {
	if err := ensureServer(); err != nil {
		return err
	}

	client, err := connectClient("", timeout)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	client.SetProject(project)

	sessionID := uuid.NewString()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if _, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   project,
		SessionId:   sessionID,
		RepoPath:    dir,
		Provider:    providerName,
		InitialCols: 80,
		InitialRows: 24,
	}); err != nil {
		return fmt.Errorf("start session: %w", err)
	}

	stream, err := client.AttachSession(ctx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  uuid.NewString(),
		AfterSeq:  0,
	})
	if err != nil {
		return fmt.Errorf("attach session: %w", err)
	}

	// Forward stdin to session; close session when stdin reaches EOF.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := os.Stdin.Read(buf)
			if n > 0 {
				_, _ = client.WriteInput(context.Background(), &bridgev1.WriteInputRequest{
					SessionId: sessionID,
					ClientId:  stream.ClientID(),
					Data:      buf[:n],
				})
			}
			if readErr != nil {
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
				_, _ = client.StopSession(stopCtx, &bridgev1.StopSessionRequest{
					SessionId: sessionID,
					Force:     true,
				})
				stopCancel()
				cancel()
				return
			}
		}
	}()

	err = stream.RecvAll(ctx, func(ev *bridgev1.AttachSessionEvent) error {
		switch ev.Type {
		case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_OUTPUT:
			_, writeErr := os.Stdout.Write(ev.Payload)
			return writeErr
		case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ERROR:
			return errors.New(ev.Error)
		default:
			return nil
		}
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("session ended: %w", err)
	}
	return nil
}

func currentTTYSize() (uint32, uint32) {
	ws, err := pty.GetsizeFull(os.Stdin)
	if err != nil {
		return 120, 40
	}
	return uint32(ws.Cols), uint32(ws.Rows)
}

func normalizeTTYInput(b []byte) []byte {
	data := append([]byte(nil), b...)
	for i := range data {
		if data[i] == '\n' {
			data[i] = '\r'
		}
	}
	return data
}
