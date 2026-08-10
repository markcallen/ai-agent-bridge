package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func touchFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
}
