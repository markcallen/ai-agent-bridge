package provider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/markcallen/ai-agent-bridge/internal/bridge"
)

// CodexProvider wraps StdioProvider with Codex-specific auth handling.
// It accepts either OPENAI_API_KEY / CODEX_API_KEY (API-key auth) or
// CODEX_AUTH (device-code auth contents) as valid authentication.
//
// When CODEX_AUTH is set, its value is written to a temporary directory
// as auth.json and the subprocess receives CODEX_HOME pointing there so
// the Codex CLI discovers the device-code credentials.
type CodexProvider struct {
	*StdioProvider

	mu      sync.Mutex
	authDir string // temp directory holding auth.json, if created
}

// NewCodexProvider creates a Codex provider that supports both API-key
// and device-code authentication. RequiredEnv is cleared from the
// underlying StdioConfig because CodexProvider validates auth itself.
func NewCodexProvider(cfg StdioConfig) *CodexProvider {
	cfg.RequiredEnv = nil // handled by CodexProvider
	return &CodexProvider{
		StdioProvider: NewStdioProvider(cfg),
	}
}

// codexHasAuth returns true if any supported Codex auth env var is set.
func codexHasAuth() bool {
	for _, key := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_AUTH"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}

func (p *CodexProvider) ValidateStartup(ctx context.Context) error {
	if !codexHasAuth() {
		return fmt.Errorf("provider %q requires OPENAI_API_KEY, CODEX_API_KEY, or CODEX_AUTH", p.cfg.ProviderID)
	}
	return p.StdioProvider.ValidateStartup(ctx)
}

func (p *CodexProvider) Health(ctx context.Context) error {
	if !codexHasAuth() {
		return fmt.Errorf("provider %q requires OPENAI_API_KEY, CODEX_API_KEY, or CODEX_AUTH", p.cfg.ProviderID)
	}
	return p.StdioProvider.Health(ctx)
}

func (p *CodexProvider) BuildCommand(ctx context.Context, cfg bridge.SessionConfig) (*exec.Cmd, error) {
	cmd, err := p.StdioProvider.BuildCommand(ctx, cfg)
	if err != nil {
		return nil, err
	}

	codexAuth := os.Getenv("CODEX_AUTH")
	if strings.TrimSpace(codexAuth) == "" {
		return cmd, nil
	}

	// Write the auth credentials to a temp directory so the Codex CLI
	// can discover them via CODEX_HOME.
	authDir, err := p.ensureAuthDir(codexAuth)
	if err != nil {
		return nil, err
	}
	cmd.Env = append(cmd.Env, "CODEX_HOME="+authDir)
	return cmd, nil
}

// ensureAuthDir creates (once) a temporary directory containing auth.json
// with the given contents. Subsequent calls return the same directory.
func (p *CodexProvider) ensureAuthDir(contents string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.authDir != "" {
		// Update the file in case the env var changed between sessions.
		authFile := filepath.Join(p.authDir, "auth.json")
		if err := os.WriteFile(authFile, []byte(contents), 0o600); err != nil {
			return "", fmt.Errorf("update codex auth file: %w", err)
		}
		return p.authDir, nil
	}

	dir, err := os.MkdirTemp("", "codex-auth-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir for codex auth: %w", err)
	}
	authFile := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authFile, []byte(contents), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("write codex auth file: %w", err)
	}
	p.authDir = dir
	return dir, nil
}

// Cleanup removes the temporary auth directory, if one was created.
func (p *CodexProvider) Cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.authDir != "" {
		_ = os.RemoveAll(p.authDir)
		p.authDir = ""
	}
}
