package main

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
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

	// Use the hostname (without port) as the TLS server name so it matches
	// the remote server's certificate SANs.
	serverName := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		serverName = h
	}

	return bridgeclient.New(
		bridgeclient.WithTarget(host),
		bridgeclient.WithTimeout(timeout),
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: caBundle,
			CertPath:     clientCert,
			KeyPath:      clientKey,
			ServerName:   serverName,
		}),
		bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: jwtKey,
			Issuer:         issuer,
			Audience:       "bridge",
		}),
	)
}

func envBridgeClient(timeout time.Duration) (*bridgeclient.Client, error) {
	target := os.Getenv("BRIDGE_ADDR")
	if target == "" {
		return nil, nil
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		// Strip brackets from bare IPv6 addresses like [2001:db8::1]
		target = strings.TrimSuffix(strings.TrimPrefix(target, "["), "]")
		target = net.JoinHostPort(target, "9445")
	}

	caBundle := os.Getenv("CA_CERT")
	certPath := os.Getenv("CLIENT_CERT")
	keyPath := os.Getenv("CLIENT_KEY")
	jwtKey := os.Getenv("JWT_KEY")
	if caBundle == "" || certPath == "" || keyPath == "" || jwtKey == "" {
		return nil, fmt.Errorf("BRIDGE_ADDR requires CA_CERT, CLIENT_CERT, CLIENT_KEY, and JWT_KEY")
	}

	serverName := os.Getenv("BRIDGE_SERVER_NAME")
	if serverName == "" {
		serverName = defaultServerName(target)
	}
	issuer := os.Getenv("JWT_ISSUER")
	if issuer == "" {
		issuer = "dev"
	}

	return bridgeclient.New(
		bridgeclient.WithTarget(target),
		bridgeclient.WithTimeout(timeout),
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: caBundle,
			CertPath:     certPath,
			KeyPath:      keyPath,
			ServerName:   serverName,
		}),
		bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: jwtKey,
			Issuer:         issuer,
			Audience:       "bridge",
		}),
	)
}

func defaultServerName(target string) string {
	host := target
	if h, _, err := net.SplitHostPort(target); err == nil {
		host = h
	}
	if host == "bridge" || host == "bridge.local" || host == "localhost" {
		return "bridge.local"
	}
	return host
}

// clientForRequest returns either a local or remote client depending on
// whether the "remote" query param is set.
func clientForRequest(r *http.Request, timeout time.Duration) (*bridgeclient.Client, error) {
	remote := r.URL.Query().Get("remote")
	if remote != "" {
		return remoteClient(remote, timeout)
	}
	if client, err := envBridgeClient(timeout); client != nil || err != nil {
		return client, err
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
