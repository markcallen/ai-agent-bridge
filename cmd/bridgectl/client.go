package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	bridgev1 "github.com/orchael/bridgectl/gen/bridge/v1"
	"github.com/orchael/bridgectl/internal/localserver"
	"github.com/orchael/bridgectl/internal/pki"
	"github.com/orchael/bridgectl/pkg/bridgeclient"
)

func newClientCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "client",
		Short: "Client credential management",
	}
	cmd.AddCommand(newClientSetupCmd())
	cmd.AddCommand(newClientInitCmd())
	cmd.AddCommand(newClientRenewCmd())
	cmd.AddCommand(newClientEnrollCmd())
	return cmd
}

func newClientEnrollCmd() *cobra.Command {
	var (
		target     string
		caBundle   string
		cert       string
		key        string
		name       string
		outDir     string
		serverName string
	)

	cmd := &cobra.Command{
		Use:   "enroll",
		Short: "Enroll this client with a remote bridge server",
		Long: `Generate a local JWT signing keypair and register the public key
with a remote bridge server. Requires a valid mTLS client certificate
(e.g. from Step CA). After enrollment, the client can authenticate
with JWT-signed tokens.

The mTLS certificate itself authorizes the enrollment — only clients
with a cert signed by the server's trusted CA can enroll.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if target == "" {
				return fmt.Errorf("--target is required")
			}
			if caBundle == "" {
				return fmt.Errorf("--ca is required")
			}
			if cert == "" {
				return fmt.Errorf("--cert is required")
			}
			if key == "" {
				return fmt.Errorf("--key is required")
			}

			// Default --name to the cert's CN.
			if name == "" {
				cn, err := extractCN(cert)
				if err != nil {
					return fmt.Errorf("--name is required (could not extract CN from cert: %v)", err)
				}
				name = cn
			}

			if outDir == "" {
				outDir = "."
			}
			target = normalizeRemoteTarget(target)
			if serverName == "" {
				serverName = defaultServerNameFromTarget(target)
			}

			// Reuse existing JWT keypair if present; generate only if either file is missing.
			pubPath := filepath.Join(outDir, "jwt-signing.pub")
			privPath := filepath.Join(outDir, "jwt-signing.key")
			_, pubErr := os.Stat(pubPath)
			_, privErr := os.Stat(privPath)
			if os.IsNotExist(pubErr) || os.IsNotExist(privErr) {
				var err error
				pubPath, privPath, err = pki.GenerateJWTKeypair(outDir, "jwt-signing")
				if err != nil {
					return fmt.Errorf("generate JWT keypair: %w", err)
				}
				fmt.Printf("Generated JWT keypair:\n")
				fmt.Printf("  Private key: %s\n", privPath)
				fmt.Printf("  Public key:  %s\n", pubPath)
			} else if pubErr != nil {
				return fmt.Errorf("check JWT public key: %w", pubErr)
			} else if privErr != nil {
				return fmt.Errorf("check JWT private key: %w", privErr)
			} else {
				fmt.Printf("Using existing JWT keypair:\n")
				fmt.Printf("  Private key: %s\n", privPath)
				fmt.Printf("  Public key:  %s\n", pubPath)
			}

			// Load the public key for the RPC.
			pubKey, err := pki.LoadEd25519PublicKey(pubPath)
			if err != nil {
				return fmt.Errorf("load public key: %w", err)
			}
			pubDER, err := x509.MarshalPKIXPublicKey(pubKey)
			if err != nil {
				return fmt.Errorf("marshal public key: %w", err)
			}

			// Connect with mTLS only (no JWT — we're enrolling to get JWT auth).
			client, err := bridgeclient.New(
				bridgeclient.WithTarget(target),
				bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
					CABundlePath: caBundle,
					CertPath:     cert,
					KeyPath:      key,
					ServerName:   serverName,
				}),
				bridgeclient.WithTimeout(10*time.Second),
			)
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer func() { _ = client.Close() }()

			fmt.Printf("\nRegistering JWT key with %s...\n", target)

			resp, err := client.RegisterJWTKey(context.Background(), &bridgev1.RegisterJWTKeyRequest{
				PublicKey: pubDER,
				Issuer:    name,
			})
			if err != nil {
				return fmt.Errorf("register key: %w", err)
			}

			fmt.Printf("Enrolled: %s\n", resp.Message)

			// Record this remote in the local remotes registry.
			// Use the hostname from the target as the display name.
			remoteName := target
			if h, _, err := net.SplitHostPort(target); err == nil {
				remoteName = h
			}
			if err := localserver.AddRemote(localserver.StateDir(), remoteName, target); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not save remote to registry: %v\n", err)
			}

			fmt.Println()
			fmt.Println("To connect with the Go SDK:")
			fmt.Println()
			fmt.Printf("  client, err := bridgeclient.New(\n")
			fmt.Printf("    bridgeclient.WithTarget(%q),\n", target)
			fmt.Printf("    bridgeclient.WithMTLS(bridgeclient.MTLSConfig{\n")
			fmt.Printf("      CABundlePath: %q,\n", caBundle)
			fmt.Printf("      CertPath:     %q,\n", cert)
			fmt.Printf("      KeyPath:      %q,\n", key)
			fmt.Printf("      ServerName:   %q,\n", serverName)
			fmt.Printf("    }),\n")
			fmt.Printf("    bridgeclient.WithJWT(bridgeclient.JWTConfig{\n")
			fmt.Printf("      PrivateKeyPath: %q,\n", privPath)
			fmt.Printf("      Issuer:         %q,\n", resp.Issuer)
			fmt.Printf("      Audience:       \"bridge\",\n")
			fmt.Printf("    }),\n")
			fmt.Printf("  )\n")

			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "bridge server address (e.g. macbook.ts.net or macbook.ts.net:9445; default port 9445)")
	_ = cmd.MarkFlagRequired("target")
	cmd.Flags().StringVar(&caBundle, "ca", "", "path to CA bundle for server verification")
	_ = cmd.MarkFlagRequired("ca")
	cmd.Flags().StringVar(&cert, "cert", "", "path to client mTLS certificate")
	_ = cmd.MarkFlagRequired("cert")
	cmd.Flags().StringVar(&key, "key", "", "path to client mTLS private key")
	_ = cmd.MarkFlagRequired("key")
	cmd.Flags().StringVar(&name, "name", "", "issuer name for JWT tokens (defaults to cert CN)")
	cmd.Flags().StringVar(&outDir, "out", "", "output directory for JWT keypair (defaults to current directory)")
	cmd.Flags().StringVar(&serverName, "server-name", "", "TLS server name to verify (defaults to host from --target)")

	return cmd
}

func defaultServerNameFromTarget(target string) string {
	if strings.HasPrefix(target, "unix://") {
		return "server"
	}
	host, _, err := net.SplitHostPort(target)
	if err == nil {
		return strings.Trim(host, "[]")
	}
	if strings.Contains(target, ":") && !strings.Contains(target, "://") {
		return "server"
	}
	return target
}

func normalizeRemoteTarget(target string) string {
	if strings.HasPrefix(target, "unix://") {
		return target
	}
	if _, _, err := net.SplitHostPort(target); err == nil {
		return target
	}
	if strings.Contains(target, ":") && !strings.Contains(target, "://") {
		return target
	}
	return target + ":" + defaultRemotePort
}

// extractCN reads a PEM certificate file and returns its Common Name.
func extractCN(certPath string) (string, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", fmt.Errorf("no PEM block found in %s", certPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	if cert.Subject.CommonName == "" {
		return "", fmt.Errorf("certificate has no CN")
	}
	return cert.Subject.CommonName, nil
}
