package provider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/orchael/bridgectl/internal/bridge"
)

// CodexProvider wraps StdioProvider with Codex-specific auth handling.
// It accepts either OPENAI_API_KEY / CODEX_API_KEY (API-key auth) or
// CODEX_AUTH / CODEX_HOME auth.json (ChatGPT account auth) as valid
// authentication.
//
// When CODEX_AUTH is set, its value is written to a stable per-user directory
// as auth.json and the subprocess receives CODEX_HOME pointing there so
// the Codex CLI discovers the device-code credentials and can persist its
// own helper binaries outside the system temp directory.
type CodexProvider struct {
	*StdioProvider

	mu      sync.Mutex
	authDir string // directory holding auth.json, if created
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

// codexHasAuth returns true if any supported Codex auth source is available.
func codexHasAuth() bool {
	for _, key := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_AUTH"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return codexAuthFilePath() != ""
}

func codexAuthFilePath() string {
	candidates := []string{}
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		candidates = append(candidates, filepath.Join(codexHome, "auth.json"))
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		candidates = append(candidates, filepath.Join(home, ".codex", "auth.json"))
	}
	for _, path := range candidates {
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func (p *CodexProvider) ValidateStartup(ctx context.Context) error {
	if !codexHasAuth() {
		return fmt.Errorf("provider %q requires OPENAI_API_KEY, CODEX_API_KEY, CODEX_AUTH, CODEX_HOME/auth.json, or ~/.codex/auth.json", p.cfg.ProviderID)
	}
	return p.StdioProvider.ValidateStartup(ctx)
}

func (p *CodexProvider) Health(ctx context.Context) error {
	if !codexHasAuth() {
		return fmt.Errorf("provider %q requires OPENAI_API_KEY, CODEX_API_KEY, CODEX_AUTH, CODEX_HOME/auth.json, or ~/.codex/auth.json", p.cfg.ProviderID)
	}
	return p.StdioProvider.Health(ctx)
}

func (p *CodexProvider) BuildCommand(ctx context.Context, cfg bridge.SessionConfig) (*exec.Cmd, error) {
	cmd, err := p.StdioProvider.BuildCommand(ctx, cfg)
	if err != nil {
		return nil, err
	}

	codexAuth := envValue(cmd.Env, "CODEX_AUTH")
	if strings.TrimSpace(codexAuth) == "" {
		return cmd, nil
	}

	// Write the auth credentials to a stable directory so the Codex CLI
	// can discover them via CODEX_HOME.
	authDir, err := p.ensureAuthDir(codexAuth, envValue(cmd.Env, "CODEX_HOME"))
	if err != nil {
		return nil, err
	}
	cmd.Env = setEnvValue(cmd.Env, "CODEX_HOME", authDir)
	return cmd, nil
}

// ensureAuthDir creates (once) a directory containing auth.json
// with the given contents. Subsequent calls return the same directory.
func (p *CodexProvider) ensureAuthDir(contents, codexHome string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.authDir != "" {
		// Update the file in case the env var changed between sessions.
		// Use atomic write (temp file + rename) so concurrent Codex
		// subprocesses never read a partially-written file.
		if err := atomicWriteFile(filepath.Join(p.authDir, "auth.json"), []byte(contents), 0o600); err != nil {
			return "", fmt.Errorf("update codex auth file: %w", err)
		}
		return p.authDir, nil
	}

	dir, err := codexAuthDir(codexHome)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create codex auth dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("secure codex auth dir: %w", err)
	}
	authFile := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authFile, []byte(contents), 0o600); err != nil {
		return "", fmt.Errorf("write codex auth file: %w", err)
	}
	p.authDir = dir
	return dir, nil
}

func codexAuthDir(codexHome string) (string, error) {
	if codexHome := strings.TrimSpace(codexHome); codexHome != "" {
		return codexHome, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("resolve codex auth dir: HOME is not available; set CODEX_HOME to a persistent directory")
	}
	return filepath.Join(home, ".config/bridgectl", "codex-home"), nil
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	replacement := prefix + value
	out := append([]string(nil), env...)
	replaced := false
	for i, item := range out {
		if strings.HasPrefix(item, prefix) {
			out[i] = replacement
			replaced = true
		}
	}
	if !replaced {
		out = append(out, replacement)
	}
	return out
}

// atomicWriteFile writes data to a temp file in the same directory as path,
// then renames it into place so readers never see a partial write.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".auth-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// Cleanup forgets the generated auth directory path. The directory itself is
// intentionally persistent because Codex stores helper binaries and session
// state under CODEX_HOME.
func (p *CodexProvider) Cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authDir = ""
}
