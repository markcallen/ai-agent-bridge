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

func TestLoadStepCAClients(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge.yaml")
	content := `
server:
  listen: "127.0.0.1:9445"
step_ca:
  url: "https://step-ca.example.internal"
  root: "/etc/bridge/step-root.crt"
  clients:
    - issuer: "do-dev2"
      key_path: "/etc/bridge/jwt-clients/do-dev2.pub"
    - issuer: "laptop-a"
      required: true
providers:
  echo:
    binary: "cat"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(cfg.StepCA.Clients); got != 2 {
		t.Fatalf("len(StepCA.Clients)=%d want 2", got)
	}
	if cfg.StepCA.Clients[0].Issuer != "do-dev2" {
		t.Fatalf("first issuer=%q want do-dev2", cfg.StepCA.Clients[0].Issuer)
	}
	if cfg.StepCA.Clients[0].KeyPath != "/etc/bridge/jwt-clients/do-dev2.pub" {
		t.Fatalf("first key_path=%q", cfg.StepCA.Clients[0].KeyPath)
	}
	if !cfg.StepCA.Clients[1].Required {
		t.Fatal("second client should be required")
	}
}

func TestLoadStepCAClientsValidateIssuer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge.yaml")
	content := `
server:
  listen: "127.0.0.1:9445"
step_ca:
  clients:
    - issuer: "../bad"
providers:
  echo:
    binary: "cat"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "step_ca.clients[0].issuer") {
		t.Fatalf("unexpected error: %v", err)
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

func TestLoadRuntimeProviderRoot(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantRoot string
		wantErr  bool
	}{
		{
			name: "provider_root set",
			content: `
server:
  listen: "127.0.0.1:9445"
auth:
  jwt_max_ttl: "5m"
runtime:
  provider_root: "/opt/ai-agent-bridge"
sessions:
  idle_timeout: "30m"
  stop_grace_period: "10s"
  subscriber_ttl: "30m"
`,
			wantRoot: "/opt/ai-agent-bridge",
		},
		{
			name: "provider_root absent",
			content: `
server:
  listen: "127.0.0.1:9445"
auth:
  jwt_max_ttl: "5m"
sessions:
  idle_timeout: "30m"
  stop_grace_period: "10s"
  subscriber_ttl: "30m"
`,
			wantRoot: "",
		},
		{
			name: "provider_root relative path rejected",
			content: `
server:
  listen: "127.0.0.1:9445"
auth:
  jwt_max_ttl: "5m"
runtime:
  provider_root: "relative/path"
sessions:
  idle_timeout: "30m"
  stop_grace_period: "10s"
  subscriber_ttl: "30m"
`,
			wantErr: true,
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
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Runtime.ProviderRoot != tc.wantRoot {
				t.Fatalf("ProviderRoot=%q want %q", cfg.Runtime.ProviderRoot, tc.wantRoot)
			}
		})
	}
}

func TestLoadValidateCertDurations(t *testing.T) {
	base := `
server:
  listen: "127.0.0.1:9445"
auth:
  jwt_max_ttl: "5m"
sessions:
  idle_timeout: "30m"
  stop_grace_period: "10s"
  subscriber_ttl: "30m"
providers:
  echo:
    binary: "cat"
`

	tests := []struct {
		name    string
		extra   string
		wantErr string
	}{
		{
			name:  "valid cert_validity",
			extra: "  cert_validity: \"30m\"\n",
		},
		{
			name:  "valid cert_renewal_check_interval",
			extra: "  cert_renewal_check_interval: \"10m\"\n",
		},
		{
			name:    "invalid cert_validity",
			extra:   "  cert_validity: \"not-a-duration\"\n",
			wantErr: "server.cert_validity",
		},
		{
			name:    "invalid cert_renewal_check_interval",
			extra:   "  cert_renewal_check_interval: \"bad\"\n",
			wantErr: "server.cert_renewal_check_interval",
		},
		{
			name:    "negative cert_validity",
			extra:   "  cert_validity: \"-5m\"\n",
			wantErr: "must not be negative",
		},
		{
			name:    "negative cert_renewal_check_interval",
			extra:   "  cert_renewal_check_interval: \"-1h\"\n",
			wantErr: "must not be negative",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Insert extra fields under the server: block.
			content := strings.Replace(base, "  listen:", tc.extra+"  listen:", 1)
			dir := t.TempDir()
			path := filepath.Join(dir, "bridge.yaml")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			_, err := Load(path)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
		})
	}
}
