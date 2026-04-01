package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewStdioProviderDefaultsAndHealth(t *testing.T) {
	p := NewStdioProvider(StdioConfig{
		ProviderID: "echo",
		Binary:     "/bin/echo",
	})

	if got := p.StartupTimeout().Seconds(); got <= 0 {
		t.Fatalf("StartupTimeout=%v want > 0", p.StartupTimeout())
	}
	if got := p.StopGrace().Seconds(); got <= 0 {
		t.Fatalf("StopGrace=%v want > 0", p.StopGrace())
	}
	if err := p.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	version, err := p.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version == "" {
		t.Fatal("Version was empty")
	}
}

func TestValidateStartupRequiredEnv(t *testing.T) {
	const key = "BRIDGE_PROVIDER_REQUIRED_ENV"
	_ = os.Unsetenv(key)

	p := NewStdioProvider(StdioConfig{
		ProviderID:   "fake",
		Binary:       "/bin/echo",
		StartupProbe: "none",
		RequiredEnv:  []string{key},
	})
	err := p.ValidateStartup(context.Background())
	if err == nil || !strings.Contains(err.Error(), key) {
		t.Fatalf("ValidateStartup error=%v want missing %s", err, key)
	}

	if err := os.Setenv(key, "present"); err != nil {
		t.Fatalf("Setenv: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	if err := p.ValidateStartup(context.Background()); err != nil {
		t.Fatalf("ValidateStartup with env: %v", err)
	}
}

func TestResolveBinaryPathAndFilterEnv(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "tool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	path, err := resolveBinaryPath(bin)
	if err != nil {
		t.Fatalf("resolveBinaryPath: %v", err)
	}
	if path != bin {
		t.Fatalf("resolveBinaryPath=%q want %q", path, bin)
	}

	env := filterEnv([]string{
		"AWS_SECRET_ACCESS_KEY=secret",
		"KEEP=value",
	})
	for _, item := range env {
		if strings.HasPrefix(item, "AWS_SECRET_ACCESS_KEY=") {
			t.Fatalf("blocked secret env leaked: %v", env)
		}
	}
	if !hasEnvKey(env, "TERM") || !hasEnvKey(env, "COLORTERM") {
		t.Fatalf("TERM and COLORTERM were not injected: %v", env)
	}
}

func TestValidateStartupOutputAndPrompt(t *testing.T) {
	script := filepath.Join(t.TempDir(), "probe.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'READY>\\n'\nsleep 1\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	outputProvider := NewStdioProvider(StdioConfig{
		ProviderID:     "output",
		Binary:         script,
		StartupProbe:   "output",
		StartupTimeout: 2 * time.Second,
	})
	if err := outputProvider.ValidateStartup(context.Background()); err != nil {
		t.Fatalf("ValidateStartup output: %v", err)
	}

	promptProvider := NewStdioProvider(StdioConfig{
		ProviderID:     "prompt",
		Binary:         script,
		StartupProbe:   "prompt",
		PromptPattern:  "READY>",
		StartupTimeout: 2 * time.Second,
	})
	if err := promptProvider.ValidateStartup(context.Background()); err != nil {
		t.Fatalf("ValidateStartup prompt: %v", err)
	}

	badProvider := NewStdioProvider(StdioConfig{
		ProviderID:   "bad",
		Binary:       "/bin/echo",
		StartupProbe: "unsupported",
	})
	if err := badProvider.ValidateStartup(context.Background()); err == nil {
		t.Fatal("ValidateStartup accepted unsupported probe")
	}
}
