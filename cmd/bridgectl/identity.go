package main

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/orchael/bridgectl/internal/localserver"
	"github.com/orchael/bridgectl/internal/pki"
)

func newIdentityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Manage local certificate identity",
	}
	cmd.AddCommand(newIdentityShowCmd())
	cmd.AddCommand(newIdentityRenewCmd())
	return cmd
}

func newIdentityShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the local certificate identity",
		Long: `Display details about the locally configured certificate identity,
including the certificate CN, issuer, expiry, and renewal status.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			stateDir := localserver.StateDir()
			mat := localserver.LoadPKIMaterial(stateDir)

			// Try client cert first, then server cert.
			certPath := mat.LocalClientCert
			if _, err := os.Stat(certPath); err != nil {
				certPath = mat.ServerCertPath
			}
			if _, err := os.Stat(certPath); err != nil {
				return fmt.Errorf("no certificate found in %s", localserver.CertsDir(stateDir))
			}

			cert, err := pki.LoadCert(certPath)
			if err != nil {
				return fmt.Errorf("load certificate: %w", err)
			}

			// Determine the provider from the PKI mode file.
			certsDir := localserver.CertsDir(stateDir)
			providerName := readPKIModeForIdentity(certsDir)

			// Calculate remaining validity.
			remaining := time.Until(cert.NotAfter)
			total := cert.NotAfter.Sub(cert.NotBefore)
			var renewalStatus string
			switch {
			case remaining > total/3:
				renewalStatus = "valid"
			case remaining > 0:
				renewalStatus = "renewal recommended"
			default:
				renewalStatus = "expired"
			}

			fingerprint := sha256.Sum256(cert.Raw)

			fmt.Printf("Identity:    %s\n", cert.Subject.CommonName)
			fmt.Printf("Provider:    %s\n", providerName)
			fmt.Printf("Issuer:      %s\n", cert.Issuer.CommonName)
			fmt.Printf("Expires:     %s\n", cert.NotAfter.Format(time.RFC3339))
			fmt.Printf("Remaining:   %s\n", remaining.Round(time.Second))
			fmt.Printf("Status:      %s\n", renewalStatus)
			fmt.Printf("Fingerprint: %x\n", fingerprint)
			fmt.Printf("Cert:        %s\n", certPath)

			return nil
		},
	}
}

func newIdentityRenewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "renew",
		Short: "Trigger immediate certificate renewal",
		Long: `Request immediate renewal of the local certificate through the
configured certificate provider. For the auto provider, this re-issues
from the local CA. For Step CA, this triggers mTLS-based renewal.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			stateDir := localserver.StateDir()
			mat := localserver.LoadPKIMaterial(stateDir)

			// Check server cert exists.
			if _, err := os.Stat(mat.ServerCertPath); err != nil {
				return fmt.Errorf("no server certificate found; run 'bridgectl server init' first")
			}

			// Derive SANs from the existing certificate so renewal preserves them.
			existingCert, err := pki.LoadCert(mat.ServerCertPath)
			if err != nil {
				return fmt.Errorf("load existing certificate: %w", err)
			}
			var sans []string
			sans = append(sans, existingCert.DNSNames...)
			for _, ip := range existingCert.IPAddresses {
				sans = append(sans, ip.String())
			}

			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

			fmt.Fprintf(os.Stderr, "Renewing server certificate...\n")
			if err := localserver.RenewServerCertMaterial(mat, sans, logger, nil, 0); err != nil {
				return fmt.Errorf("renewal failed: %w", err)
			}

			fmt.Fprintf(os.Stderr, "Certificate renewed successfully.\n")

			cert, err := pki.LoadCert(mat.ServerCertPath)
			if err != nil {
				return fmt.Errorf("load renewed certificate: %w", err)
			}
			fmt.Printf("Expires: %s\n", cert.NotAfter.Format(time.RFC3339))

			return nil
		},
	}
}

// readPKIModeForIdentity reads the PKI mode file to determine the provider name.
func readPKIModeForIdentity(certsDir string) string {
	data, err := os.ReadFile(certsDir + "/.pki-mode")
	if err != nil {
		return "auto"
	}
	mode := strings.TrimSpace(string(data))
	switch mode {
	case "auto":
		return "auto"
	case "step-ca":
		return "stepca"
	case "tls":
		return "filesystem"
	default:
		return mode
	}
}
