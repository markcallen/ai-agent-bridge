package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge.yaml")
	content := `
server:
  listen: "127.0.0.1:9445"
auth:
  jwt_max_ttl: "5m"
providers:
  echo:
    binary: "cat"
sessions:
  idle_timeout: "30m"
  stop_grace_period: "10s"
  subscriber_ttl: "30m"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Input.MaxSizeBytes == 0 {
		t.Fatal("expected default input.max_size_bytes")
	}
	if cfg.RateLimits.GlobalRPS == 0 || cfg.RateLimits.GlobalBurst == 0 {
		t.Fatal("expected default global rate limits")
	}
}

func TestLoadValidateBadDuration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge.yaml")
	content := `
server:
  listen: "127.0.0.1:9445"
auth:
  jwt_max_ttl: "bad"
providers:
  echo:
    binary: "cat"
sessions:
  idle_timeout: "30m"
  stop_grace_period: "10s"
  subscriber_ttl: "30m"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "jwt_max_ttl") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadValidateBadRequiredEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge.yaml")
	content := `
server:
  listen: "127.0.0.1:9445"
auth:
  jwt_max_ttl: "5m"
providers:
  claude:
    binary: "claude"
    required_env: [""]
sessions:
  idle_timeout: "30m"
  stop_grace_period: "10s"
  subscriber_ttl: "30m"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "required_env") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadValidateProviderFallbacks(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "accepts known fallbacks",
			content: `
server:
  listen: "127.0.0.1:9445"
auth:
  jwt_max_ttl: "5m"
providers:
  primary:
    binary: "cat"
    fallbacks: ["secondary", "tertiary"]
  secondary:
    binary: "cat"
  tertiary:
    binary: "cat"
sessions:
  idle_timeout: "30m"
  stop_grace_period: "10s"
  subscriber_ttl: "30m"
`,
		},
		{
			name: "rejects more than two fallbacks",
			content: `
server:
  listen: "127.0.0.1:9445"
auth:
  jwt_max_ttl: "5m"
providers:
  primary:
    binary: "cat"
    fallbacks: ["a", "b", "c"]
  a:
    binary: "cat"
  b:
    binary: "cat"
  c:
    binary: "cat"
sessions:
  idle_timeout: "30m"
  stop_grace_period: "10s"
  subscriber_ttl: "30m"
`,
			wantErr: "must have at most 2 entries",
		},
		{
			name: "rejects self fallback",
			content: `
server:
  listen: "127.0.0.1:9445"
auth:
  jwt_max_ttl: "5m"
providers:
  primary:
    binary: "cat"
    fallbacks: ["primary"]
sessions:
  idle_timeout: "30m"
  stop_grace_period: "10s"
  subscriber_ttl: "30m"
`,
			wantErr: "provider cannot be its own fallback",
		},
		{
			name: "rejects unknown fallback",
			content: `
server:
  listen: "127.0.0.1:9445"
auth:
  jwt_max_ttl: "5m"
providers:
  primary:
    binary: "cat"
    fallbacks: ["missing"]
sessions:
  idle_timeout: "30m"
  stop_grace_period: "10s"
  subscriber_ttl: "30m"
`,
			wantErr: `unknown provider "missing"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "bridge.yaml")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			cfg, err := Load(path)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				if got := cfg.Providers["primary"].Fallbacks; len(got) != 2 || got[0] != "secondary" || got[1] != "tertiary" {
					t.Fatalf("Fallbacks=%v want [secondary tertiary]", got)
				}
				return
			}

			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoadFeatureFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge.yaml")
	content := `
server:
  listen: "127.0.0.1:9445"
auth:
  jwt_max_ttl: "5m"
feature_flags:
  provider_fallbacks: true
providers:
  primary:
    binary: "cat"
    fallbacks: ["secondary"]
  secondary:
    binary: "cat"
sessions:
  idle_timeout: "30m"
  stop_grace_period: "10s"
  subscriber_ttl: "30m"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.FeatureFlags.ProviderFallbacks {
		t.Fatal("expected provider_fallbacks to be true")
	}
}

func TestLoadRejectsDeprecatedPTYField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge.yaml")
	content := `
server:
  listen: "127.0.0.1:9445"
auth:
  jwt_max_ttl: "5m"
providers:
  primary:
    binary: "cat"
    pty: true
sessions:
  idle_timeout: "30m"
  stop_grace_period: "10s"
  subscriber_ttl: "30m"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), ".pty is no longer supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}
