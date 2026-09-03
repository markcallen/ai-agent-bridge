package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

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
