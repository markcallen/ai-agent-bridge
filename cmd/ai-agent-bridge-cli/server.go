package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/markcallen/ai-agent-bridge/internal/localserver"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func newServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Manage the local bridge server",
	}

	cmd.AddCommand(
		newServerStartCmd(),
		newServerStatusCmd(),
		newServerStopCmd(),
	)

	return cmd
}

func newServerStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the local bridge server in the foreground",
		RunE: func(cmd *cobra.Command, args []string) error {
			if localserver.IsServerRunning("") {
				return fmt.Errorf("server already running")
			}

			srv, err := localserver.Start(localserver.Config{})
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "ai-agent-bridge server listening on %s (pid %d)\n", srv.Addr(), os.Getpid())

			// Block until signal.
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			sig := <-sigCh
			fmt.Fprintf(os.Stderr, "\nReceived %s, shutting down...\n", sig)
			srv.Stop()
			return nil
		},
	}
	return cmd
}

func newServerStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check local server status",
		RunE: func(cmd *cobra.Command, args []string) error {
			target := localserver.DiscoverTarget("")
			if target == "" {
				fmt.Println("Server: not running")
				return nil
			}

			// Read PID file.
			pidData, _ := os.ReadFile(localserver.PIDPath())
			pid := strings.TrimSpace(string(pidData))

			client, err := bridgeclient.New(bridgeclient.WithTarget(target))
			if err != nil {
				fmt.Printf("Server: stale (cannot connect to %s)\n", target)
				return nil
			}
			defer func() { _ = client.Close() }()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			resp, err := client.Health(ctx)
			if err != nil {
				fmt.Printf("Server: reachable but unhealthy (%v)\n", err)
				return nil
			}

			fmt.Printf("Server: running\n")
			fmt.Printf("  PID:         %s\n", pid)
			fmt.Printf("  Address:     %s\n", target)
			fmt.Printf("  Instance:    %s\n", resp.ServerInstanceId)
			fmt.Printf("  Providers:   %d\n", len(resp.Providers))
			for _, p := range resp.Providers {
				avail := "available"
				if !p.Available {
					avail = "unavailable"
				}
				fmt.Printf("    %-12s %s\n", p.Provider, avail)
			}
			return nil
		},
	}
	return cmd
}

func newServerStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the local bridge server",
		RunE: func(cmd *cobra.Command, args []string) error {
			pidData, err := os.ReadFile(localserver.PIDPath())
			if err != nil {
				return fmt.Errorf("no server PID file found (is the server running?)")
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
			if err != nil {
				return fmt.Errorf("invalid PID file: %w", err)
			}

			proc, err := os.FindProcess(pid)
			if err != nil {
				return fmt.Errorf("find process %d: %w", pid, err)
			}
			if err := proc.Signal(syscall.SIGTERM); err != nil {
				// SIGTERM is not supported on Windows; fall back to Kill.
				if killErr := proc.Kill(); killErr != nil {
					return fmt.Errorf("kill process %d: %w (SIGTERM failed: %v)", pid, killErr, err)
				}
				fmt.Printf("Killed server (pid %d)\n", pid)
			} else {
				fmt.Printf("Sent SIGTERM to server (pid %d)\n", pid)
			}

			// Clean up stale files after a brief wait.
			time.Sleep(500 * time.Millisecond)
			_ = os.Remove(localserver.PIDPath())
			_ = os.Remove(localserver.SocketPath())
			_ = os.Remove(localserver.AddrPath())

			return nil
		},
	}
	return cmd
}
