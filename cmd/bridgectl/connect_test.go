package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func TestDiscoverClientCertForHostnamePrefersLocalHostname(t *testing.T) {
	certsDir := t.TempDir()
	touchFile(t, filepath.Join(certsDir, "other.crt"))
	touchFile(t, filepath.Join(certsDir, "other.key"))
	touchFile(t, filepath.Join(certsDir, "do-dev2.crt"))
	touchFile(t, filepath.Join(certsDir, "do-dev2.key"))

	cert, key, err := discoverClientCertForHostname(certsDir, "do-dev2")
	if err != nil {
		t.Fatalf("discoverClientCertForHostname() error = %v", err)
	}

	if cert != filepath.Join(certsDir, "do-dev2.crt") {
		t.Fatalf("cert = %q, want hostname cert", cert)
	}
	if key != filepath.Join(certsDir, "do-dev2.key") {
		t.Fatalf("key = %q, want hostname key", key)
	}
}

func TestDiscoverClientCertForHostnameUsesShortHostname(t *testing.T) {
	certsDir := t.TempDir()
	touchFile(t, filepath.Join(certsDir, "do-dev2.crt"))
	touchFile(t, filepath.Join(certsDir, "do-dev2.key"))

	cert, key, err := discoverClientCertForHostname(certsDir, "do-dev2.example.com")
	if err != nil {
		t.Fatalf("discoverClientCertForHostname() error = %v", err)
	}

	if cert != filepath.Join(certsDir, "do-dev2.crt") {
		t.Fatalf("cert = %q, want short hostname cert", cert)
	}
	if key != filepath.Join(certsDir, "do-dev2.key") {
		t.Fatalf("key = %q, want short hostname key", key)
	}
}

func TestDiscoverClientCertForHostnameMissingHostnameKey(t *testing.T) {
	certsDir := t.TempDir()
	touchFile(t, filepath.Join(certsDir, "do-dev2.crt"))
	touchFile(t, filepath.Join(certsDir, "fallback.crt"))
	touchFile(t, filepath.Join(certsDir, "fallback.key"))

	_, _, err := discoverClientCertForHostname(certsDir, "do-dev2")
	if err == nil {
		t.Fatal("discoverClientCertForHostname() error = nil, want missing key error")
	}
	if !strings.Contains(err.Error(), "do-dev2.key") {
		t.Fatalf("error = %v, want missing hostname key path", err)
	}
}

func TestDiscoverClientCertForHostnameFallsBackToSingleCandidate(t *testing.T) {
	certsDir := t.TempDir()
	touchFile(t, filepath.Join(certsDir, "step-ca-root.crt"))
	touchFile(t, filepath.Join(certsDir, "fallback.crt"))
	touchFile(t, filepath.Join(certsDir, "fallback.key"))

	cert, key, err := discoverClientCertForHostname(certsDir, "missing-host")
	if err != nil {
		t.Fatalf("discoverClientCertForHostname() error = %v", err)
	}

	if cert != filepath.Join(certsDir, "fallback.crt") {
		t.Fatalf("cert = %q, want fallback cert", cert)
	}
	if key != filepath.Join(certsDir, "fallback.key") {
		t.Fatalf("key = %q, want fallback key", key)
	}
}

func TestRemoteAuthHintSuggestsLocalJWTKey(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	touchFile(t, filepath.Join(dir, "jwt-signing.key"))

	hint := remoteAuthHint(bridgeclient.ErrUnauthorized, "example.ts.net", "")
	if !strings.Contains(hint, "bridgectl session list --remote example.ts.net --jwt-key") {
		t.Fatalf("hint = %q, want retry command", hint)
	}
	if !strings.Contains(hint, "Remote commands use JWT keys from ~/.ai-agent-bridge/certs/ or ~/.ai-agent-bridge unless --jwt-key is set") {
		t.Fatalf("hint = %q, want credential discovery explanation", hint)
	}
	if !strings.Contains(hint, filepath.Join(dir, "jwt-signing.key")) {
		t.Fatalf("hint = %q, want absolute local key path", hint)
	}
}

func TestRemoteAuthHintSkippedWhenNotApplicable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	touchFile(t, filepath.Join(dir, "jwt-signing.key"))

	tests := []struct {
		name   string
		err    error
		remote string
		jwtKey string
	}{
		{
			name:   "local command",
			err:    bridgeclient.ErrUnauthorized,
			remote: "",
		},
		{
			name:   "explicit jwt key",
			err:    bridgeclient.ErrUnauthorized,
			remote: "example.ts.net",
			jwtKey: "/tmp/jwt-signing.key",
		},
		{
			name:   "different error",
			err:    errors.New("connection refused"),
			remote: "example.ts.net",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if hint := remoteAuthHint(tt.err, tt.remote, tt.jwtKey); hint != "" {
				t.Fatalf("remoteAuthHint() = %q, want empty", hint)
			}
		})
	}
}

func touchFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
}
