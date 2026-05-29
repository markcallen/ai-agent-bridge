package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/internal/localserver"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

// detachKey is ctrl-] (0x1d), used to detach from a session without stopping it.
const detachKey = 0x1d

func newRunCmd() *cobra.Command {
	var (
		providerName string
		project      string
		timeout      time.Duration
	)

	cmd := &cobra.Command{
		Use:   "run [directory]",
		Short: "Start an AI agent session in a directory",
		Long: `Start a local bridge server (if not already running), create a new
session with the specified provider, and attach your terminal.

If another instance is already running, the existing server is reused.

Press ctrl-] to detach from the session without stopping it.
Use 'ai-agent-bridge-cli session attach <id>' to reattach later.`,
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
			return runSession(absDir, providerName, project, timeout)
		},
	}

	cmd.Flags().StringVarP(&providerName, "provider", "p", "claude", "AI provider (claude, codex, opencode, gemini, echo)")
	cmd.Flags().StringVar(&project, "project", "local", "project ID")
	cmd.Flags().DurationVarP(&timeout, "timeout", "t", 30*time.Minute, "session timeout")

	return cmd
}

func runSession(dir, providerName, project string, timeout time.Duration) error {
	// Ensure a server is running.
	target, ownedServer, err := ensureServer()
	if err != nil {
		return err
	}

	// Connect as client.
	client, err := bridgeclient.New(
		bridgeclient.WithTarget(target),
		bridgeclient.WithTimeout(timeout),
	)
	if err != nil {
		return fmt.Errorf("connect to server: %w", err)
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
		if ownedServer != nil {
			ownedServer.Stop()
		}
		return fmt.Errorf("start session: %w", err)
	}

	// Put terminal in raw mode.
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		if ownedServer != nil {
			ownedServer.Stop()
		}
		return fmt.Errorf("stdin is not a terminal")
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		if ownedServer != nil {
			ownedServer.Stop()
		}
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
		if ownedServer != nil {
			ownedServer.Stop()
		}
		return fmt.Errorf("attach session: %w", err)
	}

	detached := false

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
						detached = true
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

	if detached {
		fmt.Fprintf(os.Stderr, "\r\nDetached from session %s\r\n", sessionID)
		fmt.Fprintf(os.Stderr, "Reattach with: ai-agent-bridge-cli session attach %s\r\n", sessionID)
	} else if err != nil {
		fmt.Fprintf(os.Stderr, "\r\nsession ended: %v\r\n", err)
	}

	// If we own the server, keep the process alive while other sessions
	// are still running — the server lives in our process, so exiting
	// would kill all sessions.
	if ownedServer != nil {
		waitForActiveSessions(client, project, ownedServer)
	}
	return nil
}

// ensureServer returns a gRPC target, starting a server if needed.
// If this process starts the server, it returns a non-nil *localserver.Server
// that the caller must Stop.
func ensureServer() (string, *localserver.Server, error) {
	// Check for existing server.
	target := localserver.DiscoverTarget("")
	if target != "" {
		return target, nil, nil
	}

	// Start a new server.
	srv, err := localserver.Start(localserver.Config{})
	if err != nil {
		return "", nil, fmt.Errorf("start local server: %w", err)
	}
	return srv.Target(), srv, nil
}

// waitForActiveSessions blocks while other sessions are still running on
// the owned server. The server lives in this process, so we must stay alive
// until all sessions finish. Ctrl-c during the wait shuts everything down.
func waitForActiveSessions(client *bridgeclient.Client, project string, srv *localserver.Server) {
	if !countActiveSessions(client, project) {
		srv.Stop()
		return
	}

	fmt.Fprintf(os.Stderr, "Waiting for other sessions to finish (ctrl-c to force shutdown)...\r\n")

	forceCh := make(chan os.Signal, 1)
	signal.Notify(forceCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(forceCh)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-forceCh:
			fmt.Fprintf(os.Stderr, "\r\nForce shutdown — stopping all sessions.\r\n")
			srv.Stop()
			return
		case <-ticker.C:
			if !countActiveSessions(client, project) {
				fmt.Fprintf(os.Stderr, "All sessions finished. Shutting down server.\r\n")
				srv.Stop()
				return
			}
		}
	}
}

// countActiveSessions returns true if the server has any sessions that are
// still running (not stopped/failed).
func countActiveSessions(client *bridgeclient.Client, project string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := client.ListSessions(ctx, &bridgev1.ListSessionsRequest{
		ProjectId: project,
	})
	if err != nil {
		return false
	}
	for _, s := range resp.Sessions {
		switch s.Status {
		case bridgev1.SessionStatus_SESSION_STATUS_STOPPED,
			bridgev1.SessionStatus_SESSION_STATUS_FAILED:
			continue
		default:
			return true
		}
	}
	return false
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
