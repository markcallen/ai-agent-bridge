package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "ai-agent-bridge")

	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build failed: %v", err)
	}

	var out bytes.Buffer
	cmd := exec.Command(bin, "--version")
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("--version exited non-zero: %v\n%s", err, out.String())
	}
	if !strings.HasPrefix(out.String(), "ai-agent-bridge ") {
		t.Errorf("unexpected --version output: %q", out.String())
	}
}
