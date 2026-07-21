package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	ca "github.com/smallstep/certificates/ca"
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
		Long: `Renew an existing client certificate using the certificate itself as
the credential — no provisioner password required.

The existing cert and key are used for mutual TLS authentication against
the Step CA /renew endpoint. The cert file is overwritten in-place; the
key is unchanged.

Use --before to only renew when the cert expires within a given window,
making this safe to call from a cron job or systemd timer:

  bridgectl client renew --before 2h`,
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

	// Build a transport: system roots + Step CA root + existing client cert (for mTLS auth).
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	rootPEM, err := os.ReadFile(rootPath)
	if err != nil {
		return fmt.Errorf("read Step CA root: %w", err)
	}
	pool.AppendCertsFromPEM(rootPEM)

	tlsCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return fmt.Errorf("load cert/key: %w", err)
	}

	// Use GetClientCertificate instead of Certificates so the cert is sent
	// unconditionally. With Certificates, Go only sends the cert when its
	// issuer DN matches what the server advertises in CertificateRequest; Step
	// CA chains (root→intermediate→leaf) cause a mismatch that silently
	// suppresses the cert, resulting in "missing client certificate" from the
	// /renew endpoint.
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs: pool,
			GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				return &tlsCert, nil
			},
			MinVersion: tls.VersionTLS12,
		},
	}

	// Create a Step CA client and renew.
	client, err := ca.NewClient(stepCAURL, ca.WithTransport(tr))
	if err != nil {
		return fmt.Errorf("create Step CA client: %w", err)
	}

	fmt.Printf("\nRenewing certificate from %s...\n", stepCAURL)
	resp, err := client.Renew(tr)
	if err != nil {
		return fmt.Errorf("renew certificate: %w", err)
	}

	// Overwrite the existing cert file in-place; key is unchanged.
	if err := localserver.WriteCertPEM(certPath, resp); err != nil {
		return fmt.Errorf("write renewed cert: %w", err)
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
