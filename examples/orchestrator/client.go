package main

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

// buildRemoteClient connects to a remote bridge server using mTLS + JWT
// credentials auto-discovered from stateDir/certs/.
func buildRemoteClient(machine, stateDir string, timeout time.Duration) (*bridgeclient.Client, error) {
	target := normalizeRemote(machine)
	certsDir := filepath.Join(stateDir, "certs")

	caBundle := filepath.Join(certsDir, "step-ca-root.crt")
	if _, err := os.Stat(caBundle); err != nil {
		return nil, fmt.Errorf("CA bundle not found at %s — run 'bridgectl client init' to enroll", caBundle)
	}

	certPath, err := discoverClientCert(certsDir)
	if err != nil {
		return nil, fmt.Errorf("no client cert in %s: %w", certsDir, err)
	}
	keyPath := strings.TrimSuffix(certPath, ".crt") + ".key"
	if _, err := os.Stat(keyPath); err != nil {
		return nil, fmt.Errorf("client key not found: %s", keyPath)
	}

	issuer, err := certCN(certPath)
	if err != nil {
		return nil, fmt.Errorf("read cert CN: %w", err)
	}

	jwtKeyPath, err := discoverJWTKey(certsDir, stateDir)
	if err != nil {
		return nil, err
	}

	return bridgeclient.New(
		bridgeclient.WithTarget(target),
		bridgeclient.WithTimeout(timeout),
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: caBundle,
			CertPath:     certPath,
			KeyPath:      keyPath,
			ServerName:   "server",
		}),
		bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: jwtKeyPath,
			Issuer:         issuer,
			Audience:       "bridge",
			TTL:            5 * time.Minute,
		}),
	)
}

func normalizeRemote(addr string) string {
	if strings.Contains(addr, ":") {
		return addr
	}
	return addr + ":9445"
}

func discoverClientCert(certsDir string) (string, error) {
	skip := map[string]bool{
		"step-ca-root.crt": true,
		"ca-bundle.crt":    true,
		"ca.crt":           true,
		"server.crt":       true,
	}
	entries, err := os.ReadDir(certsDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".crt" {
			continue
		}
		if skip[e.Name()] {
			continue
		}
		return filepath.Join(certsDir, e.Name()), nil
	}
	return "", fmt.Errorf("no client certificate found")
}

func discoverJWTKey(certsDir, stateDir string) (string, error) {
	primary := filepath.Join(certsDir, "jwt-signing.key")
	if _, err := os.Stat(primary); err == nil {
		return primary, nil
	}
	fallback := filepath.Join(stateDir, "jwt-signing.key")
	if _, err := os.Stat(fallback); err == nil {
		return fallback, nil
	}
	return "", fmt.Errorf("JWT signing key not found (checked %s and %s) — run 'bridgectl client init'", primary, fallback)
}

func certCN(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", fmt.Errorf("no PEM block in %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	return cert.Subject.CommonName, nil
}
