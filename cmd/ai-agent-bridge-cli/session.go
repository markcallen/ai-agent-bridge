package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/internal/localserver"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage agent sessions",
	}

	cmd.AddCommand(
		newSessionListCmd(),
		newSessionAttachCmd(),
		newSessionStopCmd(),
	)

	return cmd
}

func newSessionListCmd() *cobra.Command {
	var project string

	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List active sessions",
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
			target := localserver.DiscoverTarget("")
			if target == "" {
				fmt.Println("No ai-agent-bridge server running.")
				return nil
			}

			client, err := bridgeclient.New(bridgeclient.WithTarget(target))
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer func() { _ = client.Close() }()
			client.SetProject(project)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			resp, err := client.ListSessions(ctx, &bridgev1.ListSessionsRequest{
				ProjectId: project,
			})
			if err != nil {
				return fmt.Errorf("list sessions: %w", err)
			}

			if len(resp.Sessions) == 0 {
				fmt.Println("No active sessions.")
				return nil
			}

			fmt.Printf("%-36s  %-10s  %-10s  %s\n", "SESSION ID", "PROVIDER", "STATUS", "CREATED")
			for _, s := range resp.Sessions {
				status := sessionStatusString(s.Status)
				created := s.CreatedAt.AsTime().Format("15:04:05")
				fmt.Printf("%-36s  %-10s  %-10s  %s\n", s.SessionId, s.Provider, status, created)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&project, "project", "local", "project ID to filter")
	return cmd
}

func newSessionAttachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach <session-id>",
		Short: "Attach to a running session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			target := localserver.DiscoverTarget("")
			if target == "" {
				return fmt.Errorf("no ai-agent-bridge server running")
			}
			return attachSession(target, sessionID)
		},
	}
	return cmd
}

func newSessionStopCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "stop <session-id>",
		Short: "Stop a running session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			target := localserver.DiscoverTarget("")
			if target == "" {
				return fmt.Errorf("no ai-agent-bridge server running")
			}

			client, err := bridgeclient.New(bridgeclient.WithTarget(target))
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer func() { _ = client.Close() }()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err = client.StopSession(ctx, &bridgev1.StopSessionRequest{
				SessionId: sessionID,
				Force:     force,
			})
			if err != nil {
				return fmt.Errorf("stop session: %w", err)
			}
			fmt.Printf("Session %s stopped.\n", sessionID)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "force kill (SIGKILL)")
	return cmd
}

func attachSession(target, sessionID string) error {
	client, err := bridgeclient.New(
		bridgeclient.WithTarget(target),
		bridgeclient.WithTimeout(30*time.Minute),
	)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = client.Close() }()

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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	stream, err := client.AttachSession(ctx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  uuid.NewString(),
		AfterSeq:  0,
	})
	if err != nil {
		restore()
		return fmt.Errorf("attach: %w", err)
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	setupSigwinch(sigCh)
	defer signal.Stop(sigCh)

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
			cancel()
			restore()
			os.Exit(0)
		}
	}()

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
				return
			}
		}
	}()

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

func sessionStatusString(s bridgev1.SessionStatus) string {
	switch s {
	case bridgev1.SessionStatus_SESSION_STATUS_STARTING:
		return "starting"
	case bridgev1.SessionStatus_SESSION_STATUS_RUNNING:
		return "running"
	case bridgev1.SessionStatus_SESSION_STATUS_ATTACHED:
		return "attached"
	case bridgev1.SessionStatus_SESSION_STATUS_STOPPING:
		return "stopping"
	case bridgev1.SessionStatus_SESSION_STATUS_STOPPED:
		return "stopped"
	case bridgev1.SessionStatus_SESSION_STATUS_FAILED:
		return "failed"
	default:
		return "unknown"
	}
}
