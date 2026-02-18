// Command e2e-test connects to the bridge, starts a Claude session against a
// cloned repository, and verifies that it produces non-empty stdout output.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func main() {
	target := flag.String("target", "bridge:9445", "bridge address")
	cacert := flag.String("cacert", "", "CA bundle path")
	cert := flag.String("cert", "", "client cert path")
	key := flag.String("key", "", "client key path")
	jwtKey := flag.String("jwt-key", "", "JWT signing key path")
	jwtIssuer := flag.String("jwt-issuer", "e2e", "JWT issuer")
	repo := flag.String("repo", "/tmp/cache-cleaner", "repo path")
	timeout := flag.Duration("timeout", 2*time.Minute, "overall timeout")
	flag.Parse()

	os.Exit(run(*target, *cacert, *cert, *key, *jwtKey, *jwtIssuer, *repo, *timeout))
}

func run(target, cacert, cert, key, jwtKey, jwtIssuer, repo string, timeout time.Duration) int {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	baseMTLS := bridgeclient.MTLSConfig{
		CABundlePath: cacert,
		CertPath:     cert,
		KeyPath:      key,
		ServerName:   "bridge",
	}
	baseJWT := bridgeclient.JWTConfig{
		PrivateKeyPath: jwtKey,
		Issuer:         jwtIssuer,
		Audience:       "bridge",
	}

	if err := runMTLSRejectionScenarios(ctx, target, timeout, baseMTLS, baseJWT); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: mTLS rejection scenarios failed: %v\n", err)
		return 1
	}

	if err := runJWTRejectionScenarios(ctx, target, timeout, baseMTLS, jwtKey, jwtIssuer); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: JWT rejection scenarios failed: %v\n", err)
		return 1
	}

	client, err := bridgeclient.New(
		bridgeclient.WithTarget(target),
		bridgeclient.WithTimeout(timeout),
		bridgeclient.WithMTLS(baseMTLS),
		bridgeclient.WithJWT(baseJWT),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: connect: %v\n", err)
		return 1
	}
	defer client.Close()

	sessionID := uuid.NewString()
	project := "e2e"
	client.SetProject(project)

	fmt.Printf("Starting session %s (repo=%s)...\n", sessionID, repo)

	_, err = client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId: project,
		SessionId: sessionID,
		RepoPath:  repo,
		Provider:  "claude",
		AgentOpts: map[string]string{
			"arg:prompt": "What language is this project written in?",
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: start session: %v\n", err)
		return 1
	}

	stream, err := client.StreamEvents(ctx, &bridgev1.StreamEventsRequest{
		SessionId: sessionID,
		AfterSeq:  0,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: stream events: %v\n", err)
		return 1
	}

	var stdout strings.Builder
	done := make(chan int, 1)

	go func() {
		err := stream.RecvAll(ctx, func(ev *bridgev1.SessionEvent) error {
			switch ev.Type {
			case bridgev1.EventType_EVENT_TYPE_STDOUT:
				fmt.Print(ev.Text)
				stdout.WriteString(ev.Text)
			case bridgev1.EventType_EVENT_TYPE_STDERR:
				fmt.Fprint(os.Stderr, ev.Text)
			case bridgev1.EventType_EVENT_TYPE_SESSION_STOPPED:
				fmt.Println("\nSession stopped.")
				if strings.TrimSpace(stdout.String()) == "" {
					done <- 1
				} else {
					done <- 0
				}
				cancel()
			case bridgev1.EventType_EVENT_TYPE_SESSION_FAILED:
				fmt.Fprintf(os.Stderr, "\nSession FAILED: %s\n", ev.Error)
				done <- 1
				cancel()
			}
			return nil
		})
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "ERROR: recv: %v\n", err)
		}
		select {
		case done <- 1:
		default:
		}
	}()

	select {
	case code := <-done:
		return code
	case <-ctx.Done():
		fmt.Fprintf(os.Stderr, "ERROR: timed out after %s\n", timeout)
		return 1
	}
}

func runMTLSRejectionScenarios(
	ctx context.Context,
	target string,
	timeout time.Duration,
	baseMTLS bridgeclient.MTLSConfig,
	baseJWT bridgeclient.JWTConfig,
) error {
	fmt.Println("Running mTLS rejection scenarios...")

	// Case 1: bad cert (server certificate used as client identity, wrong key usage).
	if err := expectRPCFailure(
		ctx,
		target,
		timeout,
		bridgeclient.MTLSConfig{
			CABundlePath: baseMTLS.CABundlePath,
			CertPath:     "/certs/bridge.crt",
			KeyPath:      "/certs/bridge.key",
			ServerName:   baseMTLS.ServerName,
		},
		baseJWT,
		"mTLS reject: server cert as client cert",
	); err != nil {
		return err
	}

	// Case 2: wrong CA.
	badCAcert, badCAkey, err := writeRogueClientCertPair()
	if err != nil {
		return fmt.Errorf("generate wrong-CA client cert: %w", err)
	}
	if err := expectRPCFailure(
		ctx,
		target,
		timeout,
		bridgeclient.MTLSConfig{
			CABundlePath: baseMTLS.CABundlePath,
			CertPath:     badCAcert,
			KeyPath:      badCAkey,
			ServerName:   baseMTLS.ServerName,
		},
		baseJWT,
		"mTLS reject: client cert signed by wrong CA",
	); err != nil {
		return err
	}

	// Case 3: expired cert.
	expiredCert, expiredKey, err := writeExpiredClientCertPair("/certs/ca.crt", "/certs/ca.key")
	if err != nil {
		return fmt.Errorf("generate expired client cert: %w", err)
	}
	if err := expectRPCFailure(
		ctx,
		target,
		timeout,
		bridgeclient.MTLSConfig{
			CABundlePath: baseMTLS.CABundlePath,
			CertPath:     expiredCert,
			KeyPath:      expiredKey,
			ServerName:   baseMTLS.ServerName,
		},
		baseJWT,
		"mTLS reject: expired client cert",
	); err != nil {
		return err
	}

	fmt.Println("mTLS rejection scenarios passed.")
	return nil
}

func runJWTRejectionScenarios(
	ctx context.Context,
	target string,
	timeout time.Duration,
	baseMTLS bridgeclient.MTLSConfig,
	jwtKey,
	jwtIssuer string,
) error {
	fmt.Println("Running JWT rejection scenarios...")
	tests := []struct {
		name string
		jwt  bridgeclient.JWTConfig
	}{
		{
			name: "JWT reject: wrong issuer",
			jwt: bridgeclient.JWTConfig{
				PrivateKeyPath: jwtKey,
				Issuer:         jwtIssuer + "-wrong",
				Audience:       "bridge",
			},
		},
		{
			name: "JWT reject: wrong audience",
			jwt: bridgeclient.JWTConfig{
				PrivateKeyPath: jwtKey,
				Issuer:         jwtIssuer,
				Audience:       "not-bridge",
			},
		},
		{
			name: "JWT reject: expired token",
			jwt: bridgeclient.JWTConfig{
				PrivateKeyPath: jwtKey,
				Issuer:         jwtIssuer,
				Audience:       "bridge",
				TTL:            -1 * time.Minute,
			},
		},
	}

	for _, tc := range tests {
		if err := expectUnauthorizedFailure(ctx, target, timeout, baseMTLS, tc.jwt, tc.name); err != nil {
			return err
		}
	}

	fmt.Println("JWT rejection scenarios passed.")
	return nil
}

func expectRPCFailure(
	ctx context.Context,
	target string,
	timeout time.Duration,
	mtls bridgeclient.MTLSConfig,
	jwt bridgeclient.JWTConfig,
	name string,
) error {
	client, err := bridgeclient.New(
		bridgeclient.WithTarget(target),
		bridgeclient.WithTimeout(timeout),
		bridgeclient.WithMTLS(mtls),
		bridgeclient.WithJWT(jwt),
	)
	if err != nil {
		return fmt.Errorf("%s: client create failed: %w", name, err)
	}
	defer client.Close()

	client.SetProject("e2e")
	_, err = client.ListProviders(ctx)
	if err == nil {
		return fmt.Errorf("%s: expected RPC failure, got success", name)
	}
	fmt.Printf("  OK: %s\n", name)
	return nil
}

func expectUnauthorizedFailure(
	ctx context.Context,
	target string,
	timeout time.Duration,
	mtls bridgeclient.MTLSConfig,
	jwt bridgeclient.JWTConfig,
	name string,
) error {
	client, err := bridgeclient.New(
		bridgeclient.WithTarget(target),
		bridgeclient.WithTimeout(timeout),
		bridgeclient.WithMTLS(mtls),
		bridgeclient.WithJWT(jwt),
	)
	if err != nil {
		return fmt.Errorf("%s: client create failed: %w", name, err)
	}
	defer client.Close()

	client.SetProject("e2e")
	_, err = client.ListProviders(ctx)
	if !errors.Is(err, bridgeclient.ErrUnauthorized) {
		return fmt.Errorf("%s: expected unauthorized error, got: %v", name, err)
	}
	fmt.Printf("  OK: %s\n", name)
	return nil
}

func writeRogueClientCertPair() (certPath, keyPath string, err error) {
	tmpDir, err := os.MkdirTemp("", "e2e-rogue-ca-*")
	if err != nil {
		return "", "", err
	}

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	now := time.Now()
	caTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(now.UnixNano()),
		Subject:               pkix.Name{CommonName: "rogue-ca"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		return "", "", err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return "", "", err
	}

	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	clientTpl := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano() + 1),
		Subject:      pkix.Name{CommonName: "rogue-client"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTpl, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		return "", "", err
	}

	certPath = filepath.Join(tmpDir, "rogue-client.crt")
	keyPath = filepath.Join(tmpDir, "rogue-client.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}), 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)}), 0o600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func writeExpiredClientCertPair(caCertPath, caKeyPath string) (certPath, keyPath string, err error) {
	caPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return "", "", err
	}
	caBlock, _ := pem.Decode(caPEM)
	if caBlock == nil {
		return "", "", fmt.Errorf("decode CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return "", "", err
	}

	caKeyPEM, err := os.ReadFile(caKeyPath)
	if err != nil {
		return "", "", err
	}
	caKey, err := parsePrivateKeyPEM(caKeyPEM)
	if err != nil {
		return "", "", err
	}

	tmpDir, err := os.MkdirTemp("", "e2e-expired-client-*")
	if err != nil {
		return "", "", err
	}
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}

	now := time.Now()
	clientTpl := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()),
		Subject:      pkix.Name{CommonName: "expired-client"},
		NotBefore:    now.Add(-2 * time.Hour),
		NotAfter:     now.Add(-1 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	clientDER, err := x509.CreateCertificate(rand.Reader, clientTpl, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		return "", "", err
	}

	certPath = filepath.Join(tmpDir, "expired-client.crt")
	keyPath = filepath.Join(tmpDir, "expired-client.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}), 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)}), 0o600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func parsePrivateKeyPEM(p []byte) (any, error) {
	for {
		block, rest := pem.Decode(p)
		if block == nil {
			return nil, fmt.Errorf("decode private key PEM")
		}
		switch block.Type {
		case "RSA PRIVATE KEY":
			return x509.ParsePKCS1PrivateKey(block.Bytes)
		case "EC PRIVATE KEY":
			return x509.ParseECPrivateKey(block.Bytes)
		case "PRIVATE KEY":
			k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, err
			}
			switch key := k.(type) {
			case *rsa.PrivateKey:
				return key, nil
			case *ecdsa.PrivateKey:
				return key, nil
			case ed25519.PrivateKey:
				return key, nil
			default:
				return nil, fmt.Errorf("unsupported PKCS8 key type %T", k)
			}
		}
		p = rest
	}
}
