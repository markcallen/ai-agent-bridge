package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	cachedBin     string
	cachedBinDir  string
	cachedBinOnce sync.Once
	cachedBinErr  error
)

// TestMain cleans up the cached build directory after all tests complete.
func TestMain(m *testing.M) {
	code := m.Run()
	if cachedBinDir != "" {
		_ = os.RemoveAll(cachedBinDir)
	}
	os.Exit(code)
}

// buildBridgeCA compiles the bridge-ca binary once (cached across all tests)
// and returns the path.
func buildBridgeCA(t *testing.T) string {
	t.Helper()
	cachedBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "bridge-ca-test-*")
		if err != nil {
			cachedBinErr = err
			return
		}
		cachedBinDir = dir
		bin := filepath.Join(dir, "bridge-ca")
		build := exec.Command("go", "build", "-o", bin, ".")
		build.Dir = "."
		build.Stdout = os.Stdout
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			cachedBinErr = err
			return
		}
		cachedBin = bin
	})
	if cachedBinErr != nil {
		t.Fatalf("build bridge-ca: %v", cachedBinErr)
	}
	return cachedBin
}

// TestInitSubcommand verifies the init subcommand creates a CA cert and key.
// Issue #153: bridge-ca subcommand unit tests.
func TestInitSubcommand(t *testing.T) {
	bin := buildBridgeCA(t)
	outDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, "init", "--name", "test-ca", "--out", outDir)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("bridge-ca init failed: %v\nstderr: %s", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "CA certificate:") {
		t.Errorf("expected 'CA certificate:' in output, got: %s", output)
	}
	if !strings.Contains(output, "CA private key:") {
		t.Errorf("expected 'CA private key:' in output, got: %s", output)
	}

	// Verify files were created.
	certPath := filepath.Join(outDir, "ca.crt")
	keyPath := filepath.Join(outDir, "ca.key")
	if _, err := os.Stat(certPath); err != nil {
		t.Errorf("CA cert not found at %s: %v", certPath, err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("CA key not found at %s: %v", keyPath, err)
	}
}

// TestInitSubcommandMissingName verifies that init fails when --name is omitted.
func TestInitSubcommandMissingName(t *testing.T) {
	bin := buildBridgeCA(t)
	cmd := exec.Command(bin, "init")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected bridge-ca init to fail without --name")
	}
	if !strings.Contains(stderr.String(), "--name is required") {
		t.Errorf("expected '--name is required' in stderr, got: %s", stderr.String())
	}
}

// TestIssueSubcommand verifies the issue subcommand creates a certificate.
func TestIssueSubcommand(t *testing.T) {
	bin := buildBridgeCA(t)
	caDir := t.TempDir()
	certDir := t.TempDir()

	// First create a CA.
	initCmd := exec.Command(bin, "init", "--name", "test-ca", "--out", caDir)
	if err := initCmd.Run(); err != nil {
		t.Fatalf("init CA: %v", err)
	}

	// Issue a server cert.
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, "issue",
		"--type", "server",
		"--cn", "test-server",
		"--san", "localhost",
		"--ca", filepath.Join(caDir, "ca.crt"),
		"--ca-key", filepath.Join(caDir, "ca.key"),
		"--out", certDir,
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("bridge-ca issue failed: %v\nstderr: %s", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "Certificate:") {
		t.Errorf("expected 'Certificate:' in output, got: %s", output)
	}
	if !strings.Contains(output, "Private key:") {
		t.Errorf("expected 'Private key:' in output, got: %s", output)
	}
}

// TestIssueSubcommandMissingFlags verifies that issue fails when required
// flags are omitted.
func TestIssueSubcommandMissingFlags(t *testing.T) {
	bin := buildBridgeCA(t)
	cmd := exec.Command(bin, "issue")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected bridge-ca issue to fail without required flags")
	}
	if !strings.Contains(stderr.String(), "required") {
		t.Errorf("expected 'required' in stderr, got: %s", stderr.String())
	}
}

// TestIssueSubcommandInvalidType verifies that issue fails with an invalid
// --type value.
func TestIssueSubcommandInvalidType(t *testing.T) {
	bin := buildBridgeCA(t)
	caDir := t.TempDir()
	certDir := t.TempDir()

	initCmd := exec.Command(bin, "init", "--name", "test-ca", "--out", caDir)
	if err := initCmd.Run(); err != nil {
		t.Fatalf("init CA: %v", err)
	}

	cmd := exec.Command(bin, "issue",
		"--type", "invalid",
		"--cn", "test",
		"--ca", filepath.Join(caDir, "ca.crt"),
		"--ca-key", filepath.Join(caDir, "ca.key"),
		"--out", certDir,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected bridge-ca issue to fail with invalid type")
	}
	if !strings.Contains(stderr.String(), "server") || !strings.Contains(stderr.String(), "client") {
		t.Errorf("expected error about server/client type, got: %s", stderr.String())
	}
}

// TestBundleSubcommand verifies the bundle subcommand concatenates certs.
func TestBundleSubcommand(t *testing.T) {
	bin := buildBridgeCA(t)
	caDir := t.TempDir()
	bundlePath := filepath.Join(t.TempDir(), "bundle.crt")

	// Create a CA to have a cert to bundle.
	initCmd := exec.Command(bin, "init", "--name", "test-ca", "--out", caDir)
	if err := initCmd.Run(); err != nil {
		t.Fatalf("init CA: %v", err)
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, "bundle",
		"--out", bundlePath,
		filepath.Join(caDir, "ca.crt"),
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("bridge-ca bundle failed: %v\nstderr: %s", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "Trust bundle:") {
		t.Errorf("expected 'Trust bundle:' in output, got: %s", output)
	}

	if _, err := os.Stat(bundlePath); err != nil {
		t.Errorf("bundle file not found at %s: %v", bundlePath, err)
	}
}

// TestBundleSubcommandMissingFlags verifies that bundle fails when --out
// or cert paths are omitted.
func TestBundleSubcommandMissingFlags(t *testing.T) {
	bin := buildBridgeCA(t)
	cmd := exec.Command(bin, "bundle")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected bridge-ca bundle to fail without flags")
	}
	if !strings.Contains(stderr.String(), "required") {
		t.Errorf("expected 'required' in stderr, got: %s", stderr.String())
	}
}

// TestJWTKeygenSubcommand verifies the jwt-keygen subcommand creates a keypair.
func TestJWTKeygenSubcommand(t *testing.T) {
	bin := buildBridgeCA(t)
	outDir := t.TempDir()
	basePath := filepath.Join(outDir, "jwt-test")

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, "jwt-keygen", "--out", basePath)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("bridge-ca jwt-keygen failed: %v\nstderr: %s", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "Public key:") {
		t.Errorf("expected 'Public key:' in output, got: %s", output)
	}
	if !strings.Contains(output, "Private key:") {
		t.Errorf("expected 'Private key:' in output, got: %s", output)
	}

	pubPath := basePath + ".pub"
	privPath := basePath + ".key"
	if _, err := os.Stat(pubPath); err != nil {
		t.Errorf("public key not found at %s: %v", pubPath, err)
	}
	if _, err := os.Stat(privPath); err != nil {
		t.Errorf("private key not found at %s: %v", privPath, err)
	}
}

// TestUnknownSubcommand verifies that an unknown command exits non-zero.
func TestUnknownSubcommand(t *testing.T) {
	bin := buildBridgeCA(t)
	cmd := exec.Command(bin, "nonexistent")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit for unknown subcommand")
	}
}

// TestHelpSubcommand verifies that help exits with code 0.
func TestHelpSubcommand(t *testing.T) {
	bin := buildBridgeCA(t)
	cmd := exec.Command(bin, "help")
	if err := cmd.Run(); err != nil {
		t.Fatalf("bridge-ca help exited non-zero: %v", err)
	}
}

// TestNoArgsExitsNonZero verifies that running with no arguments exits non-zero.
func TestNoArgsExitsNonZero(t *testing.T) {
	bin := buildBridgeCA(t)
	cmd := exec.Command(bin)
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit with no arguments")
	}
}

// TestVerifySubcommand verifies the verify subcommand (happy path and error).
func TestVerifySubcommand(t *testing.T) {
	bin := buildBridgeCA(t)
	caDir := t.TempDir()
	certDir := t.TempDir()

	// Create a CA and issue a cert.
	initCmd := exec.Command(bin, "init", "--name", "test-ca", "--out", caDir)
	if err := initCmd.Run(); err != nil {
		t.Fatalf("init CA: %v", err)
	}

	issueCmd := exec.Command(bin, "issue",
		"--type", "server",
		"--cn", "test-server",
		"--ca", filepath.Join(caDir, "ca.crt"),
		"--ca-key", filepath.Join(caDir, "ca.key"),
		"--out", certDir,
	)
	if err := issueCmd.Run(); err != nil {
		t.Fatalf("issue cert: %v", err)
	}

	t.Run("valid cert verifies against CA", func(t *testing.T) {
		cmd := exec.Command(bin, "verify",
			"--cert", filepath.Join(certDir, "test-server.crt"),
			"--bundle", filepath.Join(caDir, "ca.crt"),
		)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			t.Fatalf("verify failed: %v", err)
		}
		if !strings.Contains(stdout.String(), "OK") {
			t.Errorf("expected 'OK' in verify output, got: %s", stdout.String())
		}
	})

	t.Run("verify with missing flags fails", func(t *testing.T) {
		cmd := exec.Command(bin, "verify")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err == nil {
			t.Fatal("expected verify to fail without flags")
		}
		if !strings.Contains(stderr.String(), "required") {
			t.Errorf("expected 'required' in stderr, got: %s", stderr.String())
		}
	})
}

// TestIssueClientCert verifies that client certificates can be issued.
func TestIssueClientCert(t *testing.T) {
	bin := buildBridgeCA(t)
	caDir := t.TempDir()
	certDir := t.TempDir()

	initCmd := exec.Command(bin, "init", "--name", "test-ca", "--out", caDir)
	if err := initCmd.Run(); err != nil {
		t.Fatalf("init CA: %v", err)
	}

	var stdout bytes.Buffer
	cmd := exec.Command(bin, "issue",
		"--type", "client",
		"--cn", "test-client",
		"--ca", filepath.Join(caDir, "ca.crt"),
		"--ca-key", filepath.Join(caDir, "ca.key"),
		"--out", certDir,
	)
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("bridge-ca issue client cert failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "Certificate:") {
		t.Errorf("expected 'Certificate:' in output")
	}
}
