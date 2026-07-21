package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/markcallen/ai-agent-bridge/internal/localserver"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

const defaultRemotePort = "9445"

// remoteCredentials holds resolved paths for a remote connection.
type remoteCredentials struct {
	caBundle string
	cert     string
	key      string
	jwtKey   string
	issuer   string
}

// connectClient discovers the local server and returns a connected
// bridgeclient.Client. It auto-detects whether the server is running in
// local (insecure) or secure (mTLS+JWT) mode and configures credentials
// accordingly.
func connectClient(stateDir string, timeout time.Duration) (*bridgeclient.Client, error) {
	if stateDir == "" {
		stateDir = localserver.StateDir()
	}

	target, mode := localserver.DiscoverTarget(stateDir)
	if target == "" {
		return nil, fmt.Errorf("no ai-agent-bridge server running")
	}

	return dialClient(target, mode, stateDir, timeout)
}

// resolveRemoteCredentials discovers mTLS+JWT credentials for a remote
// connection from ~/.ai-agent-bridge/certs/. Explicit overrides take
// precedence over auto-discovery.
//
// Discovery rules:
//   - CA bundle: step-ca-root.crt in certsDir
//   - Client cert: the first non-CA *.crt in certsDir (override with --cert)
//   - JWT key: jwt-signing.key in certsDir, then stateDir (override with --jwt-key)
//   - Issuer: CN extracted from the client cert
func resolveRemoteCredentials(certOverride, keyOverride, jwtKeyOverride string) (*remoteCredentials, error) {
	stateDir := localserver.StateDir()
	certsDir := filepath.Join(stateDir, "certs")

	creds := &remoteCredentials{}

	// CA bundle — always step-ca-root.crt for remote (Step CA) setups.
	creds.caBundle = filepath.Join(certsDir, "step-ca-root.crt")

	// Client cert: explicit override or auto-discover.
	if certOverride != "" {
		creds.cert = certOverride
		if keyOverride == "" {
			// Derive key path: replace .crt with .key.
			creds.key = strings.TrimSuffix(certOverride, ".crt") + ".key"
		} else {
			creds.key = keyOverride
		}
	} else {
		cert, key, err := discoverClientCert(certsDir)
		if err != nil {
			return nil, err
		}
		creds.cert = cert
		creds.key = key
	}

	// JWT signing key: certsDir first, then stateDir root.
	if jwtKeyOverride != "" {
		creds.jwtKey = jwtKeyOverride
	} else {
		creds.jwtKey = findJWTKey(certsDir, stateDir)
		if creds.jwtKey == "" {
			return nil, fmt.Errorf(
				"no jwt-signing.key found in %s or %s\n\nRun: bridgectl client enroll --target <host>:9445 --ca %s --cert %s --key %s",
				certsDir, stateDir, creds.caBundle, creds.cert, creds.key,
			)
		}
	}

	// Issuer: CN from the client cert.
	issuer, err := extractCN(creds.cert)
	if err != nil {
		return nil, fmt.Errorf("read client cert %s: %w\n\nRun: bridgectl client init --step-ca-url <url> --target <host>:9445", creds.cert, err)
	}
	creds.issuer = issuer

	return creds, nil
}

// discoverClientCert finds the first non-CA *.crt file in certsDir and
// returns the cert path and its matching *.key path.
func discoverClientCert(certsDir string) (cert, key string, err error) {
	entries, err := os.ReadDir(certsDir)
	if err != nil {
		return "", "", fmt.Errorf(
			"read certs dir %s: %w\n\nRun: bridgectl client init --step-ca-url <url> --target <host>:9445",
			certsDir, err,
		)
	}

	skip := map[string]bool{
		"step-ca-root.crt": true,
		"ca-bundle.crt":    true,
		"ca.crt":           true,
		"server.crt":       true,
	}

	var candidates []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".crt") {
			continue
		}
		if skip[e.Name()] {
			continue
		}
		candidates = append(candidates, e.Name())
	}

	switch len(candidates) {
	case 0:
		return "", "", fmt.Errorf(
			"no client certificate found in %s\n\nRun: bridgectl client init --step-ca-url <url> --target <host>:9445",
			certsDir,
		)
	case 1:
		base := strings.TrimSuffix(candidates[0], ".crt")
		cert = filepath.Join(certsDir, candidates[0])
		key = filepath.Join(certsDir, base+".key")
		return cert, key, nil
	default:
		names := make([]string, len(candidates))
		for i, c := range candidates {
			names[i] = filepath.Join(certsDir, c)
		}
		return "", "", fmt.Errorf(
			"multiple client certificates found in %s: %s\n\nSpecify one with --cert",
			certsDir, strings.Join(names, ", "),
		)
	}
}

// findJWTKey returns the path to jwt-signing.key, checking certsDir then
// stateDir. Returns empty string if not found in either location.
func findJWTKey(certsDir, stateDir string) string {
	for _, dir := range []string{certsDir, stateDir} {
		p := filepath.Join(dir, "jwt-signing.key")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// connectRemoteClient connects to a remote bridge server using auto-discovered
// credentials from ~/.ai-agent-bridge/certs/. Explicit overrides take
// precedence over auto-discovery.
//
// hostname may include a port (e.g. "macbook.ts.net:9445"); if no port is
// present, defaultRemotePort is used.
func connectRemoteClient(hostname string, timeout time.Duration, certOverride, keyOverride, jwtKeyOverride string) (*bridgeclient.Client, error) {
	target := hostname
	if !strings.Contains(hostname, ":") {
		target = hostname + ":" + defaultRemotePort
	}

	creds, err := resolveRemoteCredentials(certOverride, keyOverride, jwtKeyOverride)
	if err != nil {
		return nil, err
	}

	var opts []bridgeclient.Option
	opts = append(opts, bridgeclient.WithTarget(target))
	if timeout > 0 {
		opts = append(opts, bridgeclient.WithTimeout(timeout))
	}
	opts = append(opts,
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: creds.caBundle,
			CertPath:     creds.cert,
			KeyPath:      creds.key,
			ServerName:   "server",
		}),
		bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: creds.jwtKey,
			Issuer:         creds.issuer,
			Audience:       "bridge",
			TTL:            5 * time.Minute,
		}),
	)

	client, err := bridgeclient.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("connect to remote server %s: %w", target, err)
	}
	return client, nil
}

// connectClientForHost returns a client for a remote hostname (when non-empty)
// or falls back to the local server.
func connectClientForHost(remote string, timeout time.Duration, certOverride, keyOverride, jwtKeyOverride string) (*bridgeclient.Client, error) {
	if remote != "" {
		return connectRemoteClient(remote, timeout, certOverride, keyOverride, jwtKeyOverride)
	}
	return connectClient("", timeout)
}

// dialClient creates a bridgeclient for the given target and mode.
func dialClient(target string, mode localserver.ServerMode, stateDir string, timeout time.Duration) (*bridgeclient.Client, error) {
	var opts []bridgeclient.Option
	opts = append(opts, bridgeclient.WithTarget(target))
	if timeout > 0 {
		opts = append(opts, bridgeclient.WithTimeout(timeout))
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
				TTL:            5 * time.Minute,
			}),
		)
	}

	client, err := bridgeclient.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("connect to server (%s mode): %w", mode, err)
	}
	return client, nil
}
