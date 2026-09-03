package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildBridgeCA(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bridge-ca")

	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build failed: %v", err)
	}
	return bin
}

func TestVersionFlag(t *testing.T) {
	bin := buildBridgeCA(t)

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, "--version")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("--version exited non-zero: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "bridge-ca ") {
		t.Errorf("unexpected --version stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "deprecated") {
		t.Errorf("expected deprecation warning on stderr, got: %q", stderr.String())
	}
}

func TestHelpShowsDeprecation(t *testing.T) {
	bin := buildBridgeCA(t)

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, "help")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("help exited non-zero: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "DEPRECATED") {
		t.Errorf("expected DEPRECATED in usage output, got: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "deprecated") {
		t.Errorf("expected deprecation warning on stderr, got: %q", stderr.String())
	}
}

func TestNoArgsExitsNonZero(t *testing.T) {
	bin := buildBridgeCA(t)

	var stderr bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit with no args")
	}
	if !strings.Contains(stderr.String(), "deprecated") {
		t.Errorf("expected deprecation warning on stderr, got: %q", stderr.String())
	}
}
