package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/markcallen/ai-agent-bridge/internal/bridge"
)

func newTestCodexProvider() *CodexProvider {
	return NewCodexProvider(StdioConfig{
		ProviderID:   "codex",
		Binary:       "/bin/echo",
		StartupProbe: "none",
	})
}

func clearCodexEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_AUTH"} {
		t.Setenv(key, "")
		_ = os.Unsetenv(key)
	}
}

func TestCodexValidateStartup_NoAuth(t *testing.T) {
	clearCodexEnv(t)
	p := newTestCodexProvider()

	err := p.ValidateStartup(context.Background())
	if err == nil {
		t.Fatal("expected error when no auth env is set")
	}
	if !strings.Contains(err.Error(), "CODEX_AUTH") {
		t.Fatalf("error should mention CODEX_AUTH, got: %v", err)
	}
}

func TestCodexValidateStartup_OpenAIKey(t *testing.T) {
	clearCodexEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	p := newTestCodexProvider()

	if err := p.ValidateStartup(context.Background()); err != nil {
		t.Fatalf("unexpected error with OPENAI_API_KEY: %v", err)
	}
}

func TestCodexValidateStartup_CodexAPIKey(t *testing.T) {
	clearCodexEnv(t)
	t.Setenv("CODEX_API_KEY", "sk-test")
	p := newTestCodexProvider()

	if err := p.ValidateStartup(context.Background()); err != nil {
		t.Fatalf("unexpected error with CODEX_API_KEY: %v", err)
	}
}

func TestCodexValidateStartup_CodexAuth(t *testing.T) {
	clearCodexEnv(t)
	t.Setenv("CODEX_AUTH", `{"auth_mode":"tokens"}`)
	p := newTestCodexProvider()

	if err := p.ValidateStartup(context.Background()); err != nil {
		t.Fatalf("unexpected error with CODEX_AUTH: %v", err)
	}
}

func TestCodexHealth_NoAuth(t *testing.T) {
	clearCodexEnv(t)
	p := newTestCodexProvider()

	err := p.Health(context.Background())
	if err == nil {
		t.Fatal("expected error when no auth env is set")
	}
}

func TestCodexHealth_WithAPIKey(t *testing.T) {
	clearCodexEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	p := newTestCodexProvider()

	if err := p.Health(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCodexHealth_WithCodexAuth(t *testing.T) {
	clearCodexEnv(t)
	t.Setenv("CODEX_AUTH", `{"auth_mode":"tokens"}`)
	p := newTestCodexProvider()

	if err := p.Health(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCodexBuildCommand_WithCodexAuth(t *testing.T) {
	clearCodexEnv(t)
	authJSON := `{"auth_mode":"tokens","tokens":{"access_token":"test"}}`
	t.Setenv("CODEX_AUTH", authJSON)

	p := newTestCodexProvider()
	t.Cleanup(p.Cleanup)

	cfg := bridge.SessionConfig{
		ProjectID: "test",
		SessionID: "s1",
		RepoPath:  t.TempDir(),
	}
	cmd, err := p.BuildCommand(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}

	// Verify CODEX_HOME is set in the subprocess env.
	var codexHome string
	for _, e := range cmd.Env {
		if v, ok := strings.CutPrefix(e, "CODEX_HOME="); ok {
			codexHome = v
		}
	}
	if codexHome == "" {
		t.Fatal("CODEX_HOME not set in subprocess env")
	}

	// Verify auth.json was written with correct contents.
	data, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	if string(data) != authJSON {
		t.Fatalf("auth.json content = %q, want %q", string(data), authJSON)
	}

	// Verify file permissions are restrictive.
	info, err := os.Stat(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		t.Fatalf("stat auth.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("auth.json perms = %o, want 0600", perm)
	}
}

func TestCodexBuildCommand_WithAPIKey(t *testing.T) {
	clearCodexEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")

	p := newTestCodexProvider()
	t.Cleanup(p.Cleanup)

	cfg := bridge.SessionConfig{
		ProjectID: "test",
		SessionID: "s1",
		RepoPath:  t.TempDir(),
	}
	cmd, err := p.BuildCommand(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}

	// CODEX_HOME should NOT be set when using API key auth.
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "CODEX_HOME=") {
			t.Fatal("CODEX_HOME should not be set when using API key auth")
		}
	}
}

func TestCodexCleanup(t *testing.T) {
	clearCodexEnv(t)
	t.Setenv("CODEX_AUTH", `{"auth_mode":"tokens"}`)

	p := newTestCodexProvider()

	cfg := bridge.SessionConfig{
		ProjectID: "test",
		SessionID: "s1",
		RepoPath:  t.TempDir(),
	}
	_, err := p.BuildCommand(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}

	p.mu.Lock()
	dir := p.authDir
	p.mu.Unlock()
	if dir == "" {
		t.Fatal("authDir not set after BuildCommand")
	}

	p.Cleanup()

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("authDir should be removed after Cleanup, stat err: %v", err)
	}
}
