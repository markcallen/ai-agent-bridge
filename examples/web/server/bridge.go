package main

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/markcallen/ai-agent-bridge/internal/localserver"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

// localClient builds a bridgeclient for the local server via DiscoverTarget.
func localClient(stateDir string, timeout time.Duration) (*bridgeclient.Client, error) {
	target, mode := localserver.DiscoverTarget(stateDir)
	if target == "" {
		return nil, fmt.Errorf("no local bridge server found in %s", stateDir)
	}

	opts := []bridgeclient.Option{
		bridgeclient.WithTarget(target),
		bridgeclient.WithTimeout(timeout),
	}

	if mode == localserver.ModeSecure {
		mat := localserver.LoadPKIMaterial(stateDir)
		opts = append(opts,
			bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
				CABundlePath: mat.CABundlePath,
				CertPath:     mat.LocalClientCert,
				KeyPath:      mat.LocalClientKey,
				ServerName:   "server",
			}),
			bridgeclient.WithJWT(bridgeclient.JWTConfig{
				PrivateKeyPath: mat.JWTSigningKey,
				Issuer:         "local",
				Audience:       "bridge",
			}),
		)
	}

	return bridgeclient.New(opts...)
}

// remoteClient builds a bridgeclient for a remote Step CA server.
// host is hostname or host:port (default port 9445).
// Credentials are auto-discovered from ~/.ai-agent-bridge/certs/.
func remoteClient(host string, timeout time.Duration) (*bridgeclient.Client, error) {
	// Normalise host to include port
	if !strings.Contains(host, ":") {
		host = host + ":9445"
	}

	stateDir := localserver.StateDir()
	certsDir := localserver.CertsDir(stateDir)

	// CA bundle
	caBundle := filepath.Join(certsDir, "step-ca-root.crt")
	if _, err := os.Stat(caBundle); err != nil {
		// Fall back to generic ca-bundle.crt
		caBundle = filepath.Join(certsDir, "ca-bundle.crt")
	}

	// Client cert: first non-CA *.crt in certsDir
	clientCert, clientKey, err := findClientCert(certsDir)
	if err != nil {
		return nil, fmt.Errorf("find client cert: %w", err)
	}

	// JWT signing key
	jwtKey := filepath.Join(certsDir, "jwt-signing.key")
	if _, err := os.Stat(jwtKey); err != nil {
		jwtKey = filepath.Join(stateDir, "jwt-signing.key")
		if _, err2 := os.Stat(jwtKey); err2 != nil {
			return nil, fmt.Errorf("jwt signing key not found in %s or %s", certsDir, stateDir)
		}
	}

	// Issuer: CN from client cert
	issuer, err := certCN(clientCert)
	if err != nil {
		return nil, fmt.Errorf("read client cert CN: %w", err)
	}

	return bridgeclient.New(
		bridgeclient.WithTarget(host),
		bridgeclient.WithTimeout(timeout),
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: caBundle,
			CertPath:     clientCert,
			KeyPath:      clientKey,
			ServerName:   "server",
		}),
		bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: jwtKey,
			Issuer:         issuer,
			Audience:       "bridge",
		}),
	)
}

// clientForRequest returns either a local or remote client depending on
// whether the "remote" query param is set.
func clientForRequest(r *http.Request, timeout time.Duration) (*bridgeclient.Client, error) {
	remote := r.URL.Query().Get("remote")
	if remote != "" {
		return remoteClient(remote, timeout)
	}
	return localClient(localserver.StateDir(), timeout)
}

// findClientCert scans certsDir for the first non-CA *.crt file and returns
// the cert path and corresponding key path (same name with .key extension).
func findClientCert(certsDir string) (cert, key string, err error) {
	entries, err := os.ReadDir(certsDir)
	if err != nil {
		return "", "", fmt.Errorf("read certs dir %s: %w", certsDir, err)
	}

	skipNames := map[string]bool{
		"step-ca-root.crt": true,
		"ca-bundle.crt":    true,
		"ca.crt":           true,
		"server.crt":       true,
		"local-client.crt": true,
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".crt") {
			continue
		}
		if skipNames[e.Name()] {
			continue
		}
		certPath := filepath.Join(certsDir, e.Name())
		keyPath := strings.TrimSuffix(certPath, ".crt") + ".key"
		if _, kerr := os.Stat(keyPath); kerr == nil {
			return certPath, keyPath, nil
		}
	}
	return "", "", fmt.Errorf("no client certificate found in %s", certsDir)
}

// certCN returns the Common Name from a PEM-encoded certificate file.
func certCN(certPath string) (string, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", fmt.Errorf("no PEM block in %s", certPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	return cert.Subject.CommonName, nil
}
