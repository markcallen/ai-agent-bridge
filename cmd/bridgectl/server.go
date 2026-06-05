package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/markcallen/ai-agent-bridge/internal/localserver"
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
		newServerIssueClientCmd(),
	)

	return cmd
}

func newServerStartCmd() *cobra.Command {
	var (
		listenAddr string
		serverSANs []string
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the bridge server in the foreground",
		Long: `Start the bridge server. By default it runs in local mode on a unix
socket with no authentication. Use --listen to bind to a TCP address
with mTLS + JWT for remote access (e.g. over a WireGuard VPN).

In secure mode, PKI material (CA, server cert, JWT keypair) is
auto-generated on first start and stored in ~/.ai-agent-bridge/certs/.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if localserver.IsServerRunning("") {
				return fmt.Errorf("server already running")
			}

			cfg := localserver.Config{
				ListenAddr: listenAddr,
				ServerSANs: serverSANs,
			}

			if listenAddr != "" {
				cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
			}

			srv, err := localserver.Start(cfg)
			if err != nil {
				return err
			}

			mode := "local (unix socket, no auth)"
			if listenAddr != "" {
				mode = fmt.Sprintf("secure (mTLS+JWT on %s)", srv.Addr())
			}
			fmt.Fprintf(os.Stderr, "ai-agent-bridge server listening — %s (pid %d)\n", mode, os.Getpid())

			// Block until signal.
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			sig := <-sigCh
			fmt.Fprintf(os.Stderr, "\nReceived %s, shutting down...\n", sig)
			srv.Stop()
			return nil
		},
	}

	cmd.Flags().StringVar(&listenAddr, "listen", "", "TCP address for secure mode (e.g. 10.0.0.1:9445 or 0.0.0.0:9445)")
	cmd.Flags().StringSliceVar(&serverSANs, "san", nil, "additional server cert SANs (DNS names or IPs)")

	return cmd
}

func newServerStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check server status",
		RunE: func(cmd *cobra.Command, args []string) error {
			target, mode := localserver.DiscoverTarget("")
			if target == "" {
				fmt.Println("Server: not running")
				return nil
			}

			// Read PID file.
			pidData, _ := os.ReadFile(localserver.PIDPath())
			pid := strings.TrimSpace(string(pidData))

			client, err := connectClient("", 3*time.Second)
			if err != nil {
				fmt.Printf("Server: stale (cannot connect to %s)\n", target)
				return nil
			}
			defer func() { _ = client.Close() }()

			resp, err := client.Health(cmd.Context())
			if err != nil {
				fmt.Printf("Server: reachable but unhealthy (%v)\n", err)
				return nil
			}

			fmt.Printf("Server: running\n")
			fmt.Printf("  Mode:        %s\n", mode)
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
		Short: "Stop the bridge server",
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

			// Wait until the server is actually down before cleaning up
			// state files.
			for i := 0; i < 30; i++ {
				time.Sleep(200 * time.Millisecond)
				if !localserver.IsServerRunning("") {
					break
				}
			}

			// Only remove state files if the server is no longer responding.
			if !localserver.IsServerRunning("") {
				_ = os.Remove(localserver.PIDPath())
				_ = os.Remove(localserver.SocketPath())
				_ = os.Remove(localserver.AddrPath())
				stateDir := localserver.StateDir()
				_ = os.Remove(filepath.Join(stateDir, "server.mode"))
			} else {
				fmt.Fprintf(os.Stderr, "Warning: server still responding after SIGTERM; state files not removed\n")
			}

			return nil
		},
	}
	return cmd
}

func newServerIssueClientCmd() *cobra.Command {
	var clientName string

	cmd := &cobra.Command{
		Use:   "issue-client",
		Short: "Issue a client certificate for a remote machine",
		Long: `Generate a client certificate and JWT keypair for a remote machine.
Each client gets its own signing key so credentials can be rotated or
revoked independently. The remote machine needs these files:

  1. CA bundle      (ca-bundle.crt)   — to verify the server
  2. Client cert    (<name>.crt)      — to authenticate to the server
  3. Client key     (<name>.key)      — private key for the cert
  4. JWT signing key (jwt-signing.key) — per-client key to mint tokens

Copy these files to the remote machine and use them with the Go SDK.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if clientName == "" {
				return fmt.Errorf("--name is required")
			}

			stateDir := localserver.StateDir()
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

			certPath, keyPath, err := localserver.IssueClientCert(stateDir, clientName, logger)
			if err != nil {
				return err
			}

			mat := localserver.LoadPKIMaterial(stateDir)
			clientDir := filepath.Join(localserver.CertsDir(stateDir), "clients", clientName)
			clientJWTKey := filepath.Join(clientDir, "jwt-signing.key")

			fmt.Println("Client credentials issued successfully.")
			fmt.Println()
			fmt.Println("Copy these files to the remote machine:")
			fmt.Printf("  CA bundle:       %s\n", mat.CABundlePath)
			fmt.Printf("  Client cert:     %s\n", certPath)
			fmt.Printf("  Client key:      %s\n", keyPath)
			fmt.Printf("  JWT signing key: %s\n", clientJWTKey)
			fmt.Println()
			fmt.Println("The server will accept tokens from this client on next restart.")
			fmt.Println("If the server is already running, restart it to load the new key.")
			fmt.Println()
			fmt.Println("Example Go SDK usage:")
			fmt.Println()
			fmt.Printf("  client, err := bridgeclient.New(\n")
			fmt.Printf("    bridgeclient.WithTarget(\"<server-addr>:9445\"),\n")
			fmt.Printf("    bridgeclient.WithMTLS(bridgeclient.MTLSConfig{\n")
			fmt.Printf("      CABundlePath: \"ca-bundle.crt\",\n")
			fmt.Printf("      CertPath:     \"%s.crt\",\n", clientName)
			fmt.Printf("      KeyPath:      \"%s.key\",\n", clientName)
			fmt.Printf("      ServerName:   \"server\",\n")
			fmt.Printf("    }),\n")
			fmt.Printf("    bridgeclient.WithJWT(bridgeclient.JWTConfig{\n")
			fmt.Printf("      PrivateKeyPath: \"jwt-signing.key\",\n")
			fmt.Printf("      Issuer:         \"%s\",\n", clientName)
			fmt.Printf("      Audience:       \"bridge\",\n")
			fmt.Printf("    }),\n")
			fmt.Printf("  )\n")

			return nil
		},
	}

	cmd.Flags().StringVar(&clientName, "name", "", "client name (used as cert CN and filenames)")
	_ = cmd.MarkFlagRequired("name")

	return cmd
}
