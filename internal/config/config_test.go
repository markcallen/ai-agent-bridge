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
