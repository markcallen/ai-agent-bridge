package localserver

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
)

// DeployBundle copies a credential bundle to a remote host via scp.
// The target format is "host:path" (e.g. "do-dev2:~/bridge-creds/").
// Uses the system scp binary; requires SSH keys or agent to be configured.
func DeployBundle(bundlePath, target string, logger *slog.Logger) error {
	scpBin, err := exec.LookPath("scp")
	if err != nil {
		return fmt.Errorf("scp not found on PATH: %w", err)
	}

	logger.Info("deploying credential bundle", "bundle", bundlePath, "target", target)
	cmd := exec.Command(scpBin, bundlePath, target)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("scp failed: %w", err)
	}
	return nil
}
