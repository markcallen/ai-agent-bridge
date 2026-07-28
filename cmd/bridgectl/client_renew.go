package main

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/markcallen/ai-agent-bridge/internal/localserver"
)

func newClientRenewCmd() *cobra.Command {
	var (
		stepCAURL string
		rootPath  string
		certPath  string
		keyPath   string
		before    time.Duration
	)

	cmd := &cobra.Command{
		Use:   "renew",
		Short: "Renew a client certificate from Step CA",
		Long: `Renew an existing client certificate via the Step CA /renew endpoint.

Uses the 'step' CLI to perform renewal with a signed token (the same
mechanism as 'step ca renew'). The cert file is overwritten in-place;
the key is unchanged.

Use --before to only renew when the cert expires within a given window,
making this safe to call from a cron job or systemd timer:

  bridgectl client renew --step-ca-url https://step-ca.example.com --before 2h`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClientRenew(stepCAURL, rootPath, certPath, keyPath, before)
		},
	}

	stateDir := localserver.StateDir()
	certsDir := filepath.Join(stateDir, "certs")

	cmd.Flags().StringVar(&stepCAURL, "step-ca-url", "", "Step CA server URL (e.g. https://step-ca-dev.example.com)")
	_ = cmd.MarkFlagRequired("step-ca-url")
	cmd.Flags().StringVar(&rootPath, "step-ca-root", filepath.Join(certsDir, "step-ca-root.crt"), "path to Step CA root certificate")
	cmd.Flags().StringVar(&certPath, "cert", "", "path to client certificate to renew (default: <state-dir>/certs/<hostname>.crt)")
	cmd.Flags().StringVar(&keyPath, "key", "", "path to client private key (default: <state-dir>/certs/<hostname>.key)")
	cmd.Flags().DurationVar(&before, "before", 0, "only renew if the cert expires within this duration (e.g. 2h); 0 always renews")

	return cmd
}

func runClientRenew(stepCAURL, rootPath, certPath, keyPath string, before time.Duration) error {
	stateDir := localserver.StateDir()
	certsDir := filepath.Join(stateDir, "certs")

	// Default cert/key to <state-dir>/certs/<hostname>.{crt,key}
	if certPath == "" || keyPath == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("resolve default cert path: %w", err)
		}
		if certPath == "" {
			certPath = filepath.Join(certsDir, hostname+".crt")
		}
		if keyPath == "" {
			keyPath = filepath.Join(certsDir, hostname+".key")
		}
	}

	// Parse the existing cert to check expiry and show info.
	existing, err := loadLeafCert(certPath)
	if err != nil {
		return fmt.Errorf("read existing cert: %w", err)
	}

	fmt.Printf("Current certificate:\n")
	fmt.Printf("  CN:      %s\n", existing.Subject.CommonName)
	fmt.Printf("  Expires: %s (%s)\n", existing.NotAfter.Format(time.RFC3339), timeUntil(existing.NotAfter))

	// --before: skip if not expiring soon enough.
	if before > 0 && time.Until(existing.NotAfter) > before {
		fmt.Printf("\nCert not due for renewal (expires in %s, threshold %s). Nothing to do.\n",
			timeUntil(existing.NotAfter), before)
		return nil
	}

	stepBin, err := exec.LookPath("step")
	if err != nil {
		return fmt.Errorf("'step' CLI not found on PATH — required for certificate renewal; install from https://smallstep.com/cli/: %w", err)
	}

	fmt.Printf("\nRenewing certificate from %s...\n", stepCAURL)

	// step ca renew uses a signed token (not mTLS) by default, which works
	// regardless of whether the Step CA server requests client certs at the
	// TLS level.
	args := []string{
		"ca", "renew",
		"--ca-url", stepCAURL,
		"--root", rootPath,
		"--force",
		certPath,
		keyPath,
	}
	cmd := exec.Command(stepBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("step ca renew: %w", err)
	}

	renewed, err := loadLeafCert(certPath)
	if err != nil {
		return fmt.Errorf("read renewed cert: %w", err)
	}

	fmt.Printf("\nRenewed certificate:\n")
	fmt.Printf("  Cert:    %s\n", certPath)
	fmt.Printf("  Expires: %s (%s)\n", renewed.NotAfter.Format(time.RFC3339), timeUntil(renewed.NotAfter))

	return nil
}

// loadLeafCert reads a PEM cert file and returns the first (leaf) certificate.
func loadLeafCert(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}
	return x509.ParseCertificate(block.Bytes)
}

func timeUntil(t time.Time) string {
	d := time.Until(t)
	if d < 0 {
		return "EXPIRED"
	}
	h := int(d.Hours())
	switch {
	case h >= 24:
		return fmt.Sprintf("%dd", h/24)
	case h >= 1:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}
