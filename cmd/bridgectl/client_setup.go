package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/orchael/bridgectl/internal/localserver"
)

func newClientSetupCmd() *cobra.Command {
	var bundlePath string

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Initialize client credentials directory and extract a bundle",
		Long: `Create the client credentials directory (~/.config/bridgectl/certs/)
and optionally extract a credential bundle into it.

Run this on the client machine before 'bridgectl client enroll'.
If --bundle is given, the .tar.gz is extracted into the certs directory.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			stateDir := localserver.StateDir()
			certsDir := localserver.CertsDir(stateDir)

			if err := os.MkdirAll(certsDir, 0o700); err != nil {
				return fmt.Errorf("create certs directory: %w", err)
			}
			fmt.Printf("Credentials directory: %s\n", certsDir)

			if bundlePath != "" {
				if _, err := os.Stat(bundlePath); err != nil {
					return fmt.Errorf("bundle not found: %w", err)
				}

				tarBin, err := exec.LookPath("tar")
				if err != nil {
					return fmt.Errorf("tar not found on PATH: %w", err)
				}

				tarCmd := exec.Command(tarBin, "xzf", bundlePath, "-C", certsDir)
				tarCmd.Stdout = os.Stdout
				tarCmd.Stderr = os.Stderr
				if err := tarCmd.Run(); err != nil {
					return fmt.Errorf("extract bundle: %w", err)
				}

				fmt.Printf("Extracted %s into %s\n", filepath.Base(bundlePath), certsDir)

				// List what was extracted.
				entries, _ := os.ReadDir(certsDir)
				for _, e := range entries {
					fmt.Printf("  %s\n", e.Name())
				}
			}

			fmt.Println()
			fmt.Println("Next steps:")
			fmt.Println("  bridgectl client enroll --target <server>:9445")

			return nil
		},
	}

	cmd.Flags().StringVar(&bundlePath, "bundle", "", "path to a credential .tar.gz bundle to extract")

	return cmd
}
