package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/orchael/bridgectl/internal/enrollment"
	"github.com/orchael/bridgectl/internal/localserver"
)

func newEnrollmentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enrollment",
		Short: "Manage enrollment tokens",
	}
	cmd.AddCommand(newEnrollmentCreateCmd())
	return cmd
}

func newEnrollmentCreateCmd() *cobra.Command {
	var (
		identity string
		expires  time.Duration
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a one-time enrollment token",
		Long: `Create a one-time enrollment token that a client can use to bootstrap
its identity with the bridge server.

The token expires after the specified duration (default 15 minutes) and
can only be used once.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if identity == "" {
				return fmt.Errorf("--identity is required")
			}

			stateDir := localserver.StateDir()
			store, err := enrollment.NewStore(stateDir)
			if err != nil {
				return fmt.Errorf("open enrollment store: %w", err)
			}

			tok, err := enrollment.Generate(identity, expires, "", "")
			if err != nil {
				return fmt.Errorf("generate enrollment token: %w", err)
			}

			if err := store.Put(tok); err != nil {
				return fmt.Errorf("store enrollment token: %w", err)
			}

			fmt.Fprintf(os.Stderr, "Enrollment created.\n\n")
			fmt.Fprintf(os.Stderr, "  Identity: %s\n", tok.Identity)
			fmt.Fprintf(os.Stderr, "  Expires:  %s (%s)\n", tok.ExpiresAt.Format(time.RFC3339), expires)
			fmt.Fprintf(os.Stderr, "\n")
			// Print the token to stdout so it can be piped.
			fmt.Println(tok.Value)

			return nil
		},
	}

	cmd.Flags().StringVar(&identity, "identity", "", "intended certificate CN for the enrolling client (required)")
	cmd.Flags().DurationVar(&expires, "expires", enrollment.DefaultTokenExpiry, "token lifetime (e.g. 15m, 1h)")

	return cmd
}
