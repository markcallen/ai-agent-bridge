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

If another instance is already running, the existing server is reused.`,
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
	if ownedServer != nil {
		defer ownedServer.Stop()
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
		return fmt.Errorf("start session: %w", err)
	}

	// Put terminal in raw mode.
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("stdin is not a terminal")
	}
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
			// SIGINT/SIGTERM → stop session.
			cancel()
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
			_, _ = client.StopSession(stopCtx, &bridgev1.StopSessionRequest{
				SessionId: sessionID,
				Force:     true,
			})
			stopCancel()
			restore()
			os.Exit(0)
		}
	}()

	// Forward stdin → session.
	go func() {
		buf := make([]byte, 1024)
		for {
			n, readErr := os.Stdin.Read(buf)
			if n > 0 {
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
	if err != nil {
		fmt.Fprintf(os.Stderr, "\r\nsession ended: %v\r\n", err)
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
