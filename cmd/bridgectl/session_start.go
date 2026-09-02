package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

func newSessionStartCmd() *cobra.Command {
	var (
		providerName string
		project      string
		timeout      time.Duration
		noTTY        bool
		remote       string
		cert         string
		key          string
		jwtKey       string
		serverName   string
	)

	cmd := &cobra.Command{
		Use:   "start [directory]",
		Short: "Start and attach to a new AI agent session",
		Long: `Start a new AI agent session and attach your terminal.

Without --remote, this auto-starts a local bridge server (if not already
running), creates a session, and attaches — the same behavior as the
deprecated 'bridgectl run' command.

With --remote, this connects to a remote bridge server using mTLS + JWT
credentials auto-discovered from ~/.config/bridgectl/certs/, creates a
session via StartSession RPC, and attaches your terminal.

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

			if remote != "" {
				if noTTY {
					return runRemoteSessionNoTTY(absDir, providerName, project, timeout, remote, cert, key, jwtKey, serverName)
				}
				return runRemoteSession(absDir, providerName, project, timeout, remote, cert, key, jwtKey, serverName)
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
	addRemoteFlags(cmd, &remote, &cert, &key, &jwtKey, &serverName)

	return cmd
}
