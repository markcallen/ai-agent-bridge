package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/markcallen/ai-agent-bridge/internal/bridge"
)

// StdioConfig configures an interactive PTY-backed provider.
type StdioConfig struct {
	ProviderID     string
	Binary         string
	DefaultArgs    []string
	StartupTimeout time.Duration
	StopGrace      time.Duration
	StartupProbe   string
	PromptPattern  string
	RequiredEnv    []string
	StreamJSON     bool // if true, the provider uses stream-JSON mode (no PTY)
}

// StdioProvider defines how to launch and validate one interactive CLI.
type StdioProvider struct {
	cfg      StdioConfig
	promptRe *regexp.Regexp
}

func NewStdioProvider(cfg StdioConfig) *StdioProvider {
	if cfg.StartupTimeout <= 0 {
		cfg.StartupTimeout = 45 * time.Second
	}
	if cfg.StopGrace <= 0 {
		cfg.StopGrace = 10 * time.Second
	}
	if cfg.StartupProbe == "" {
		cfg.StartupProbe = "prompt"
	}
	p := &StdioProvider{cfg: cfg}
	if cfg.PromptPattern != "" {
		p.promptRe = regexp.MustCompile(cfg.PromptPattern)
	}
	return p
}

func (p *StdioProvider) ID() string                    { return p.cfg.ProviderID }
func (p *StdioProvider) Binary() string                { return p.cfg.Binary }
func (p *StdioProvider) PromptPattern() *regexp.Regexp { return p.promptRe }
func (p *StdioProvider) StartupTimeout() time.Duration { return p.cfg.StartupTimeout }
func (p *StdioProvider) StopGrace() time.Duration      { return p.cfg.StopGrace }

// IsStreamJSON implements bridge.StreamJSONProvider. It returns true when the
// provider is configured with StreamJSON: true (i.e. it emits JSONL on stdout
// instead of raw PTY bytes).
func (p *StdioProvider) IsStreamJSON() bool { return p.cfg.StreamJSON }

func (p *StdioProvider) BuildCommand(ctx context.Context, cfg bridge.SessionConfig) (*exec.Cmd, error) {
	binPath, err := resolveBinaryPath(p.cfg.Binary)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve binary %q: %v", bridge.ErrProviderUnavailable, p.cfg.Binary, err)
	}
	args, err := resolveCommandArgs(p.cfg.DefaultArgs)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve args for %q: %v", bridge.ErrProviderUnavailable, p.cfg.ProviderID, err)
	}
	for key, value := range cfg.Options {
		if strings.HasPrefix(key, "arg:") {
			args = append(args, value)
		}
	}
	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = cfg.RepoPath
	cmd.Env = filterEnv(os.Environ())
	return cmd, nil
}

func (p *StdioProvider) ValidateStartup(ctx context.Context) error {
	for _, envName := range p.cfg.RequiredEnv {
		if strings.TrimSpace(os.Getenv(envName)) == "" {
			return fmt.Errorf("provider %q requires env var %q", p.cfg.ProviderID, envName)
		}
	}
	if p.promptRe == nil {
		if p.cfg.StartupProbe == "prompt" {
			return nil
		}
	}
	switch p.cfg.StartupProbe {
	case "none":
		return nil
	case "output":
		return p.validateStartupOutput(ctx)
	case "prompt":
		return p.validateStartupPrompt(ctx)
	default:
		return fmt.Errorf("provider %q has unsupported startup probe %q", p.cfg.ProviderID, p.cfg.StartupProbe)
	}
}

func (p *StdioProvider) validateStartupPrompt(ctx context.Context) error {
	if p.promptRe == nil {
		return nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, p.cfg.StartupTimeout)
	defer cancel()

	binPath, err := resolveBinaryPath(p.cfg.Binary)
	if err != nil {
		return err
	}
	args, err := resolveCommandArgs(p.cfg.DefaultArgs)
	if err != nil {
		return err
	}
	wd, _ := os.Getwd()
	cmd := exec.CommandContext(probeCtx, binPath, args...)
	cmd.Dir = wd
	cmd.Env = filterEnv(os.Environ())

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 40})
	if err != nil {
		return fmt.Errorf("provider %q startup probe: %w", p.cfg.ProviderID, err)
	}
	defer func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
	}()

	buf := make([]byte, 4096)
	var seen bytes.Buffer
	for {
		if probeCtx.Err() != nil {
			return fmt.Errorf("provider %q startup probe timed out waiting for prompt %q; output:\n%s", p.cfg.ProviderID, p.cfg.PromptPattern, seen.String())
		}
		_ = ptmx.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, readErr := ptmx.Read(buf)
		if n > 0 {
			seen.Write(buf[:n])
			if p.promptRe.Match(seen.Bytes()) {
				return nil
			}
		}
		if readErr != nil {
			if os.IsTimeout(readErr) {
				continue
			}
			return fmt.Errorf("provider %q startup probe failed: %v; output:\n%s", p.cfg.ProviderID, readErr, seen.String())
		}
	}
}

func (p *StdioProvider) validateStartupOutput(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, p.cfg.StartupTimeout)
	defer cancel()

	binPath, err := resolveBinaryPath(p.cfg.Binary)
	if err != nil {
		return err
	}
	args, err := resolveCommandArgs(p.cfg.DefaultArgs)
	if err != nil {
		return err
	}
	wd, _ := os.Getwd()
	cmd := exec.CommandContext(probeCtx, binPath, args...)
	cmd.Dir = wd
	cmd.Env = filterEnv(os.Environ())

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 40})
	if err != nil {
		return fmt.Errorf("provider %q startup probe: %w", p.cfg.ProviderID, err)
	}
	defer func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
	}()

	buf := make([]byte, 4096)
	var seen bytes.Buffer
	for {
		if probeCtx.Err() != nil {
			return fmt.Errorf("provider %q startup probe timed out waiting for output; output:\n%s", p.cfg.ProviderID, seen.String())
		}
		_ = ptmx.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, readErr := ptmx.Read(buf)
		if n > 0 {
			seen.Write(buf[:n])
			time.Sleep(250 * time.Millisecond)
			return nil
		}
		if readErr != nil {
			if os.IsTimeout(readErr) {
				continue
			}
			return fmt.Errorf("provider %q startup probe failed: %v; output:\n%s", p.cfg.ProviderID, readErr, seen.String())
		}
	}
}

func (p *StdioProvider) Version(ctx context.Context) (string, error) {
	path, err := resolveBinaryPath(p.cfg.Binary)
	if err != nil {
		return "", fmt.Errorf("binary %q not found: %w", p.cfg.Binary, err)
	}
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = filterEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("version check: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (p *StdioProvider) Health(ctx context.Context) error {
	path, err := resolveBinaryPath(p.cfg.Binary)
	if err != nil {
		return fmt.Errorf("binary %q not found: %w", p.cfg.Binary, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %q: %w", path, err)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("binary %q is not executable", path)
	}
	return nil
}

func resolveBinaryPath(binary string) (string, error) {
	if strings.Contains(binary, "/") {
		if filepath.IsAbs(binary) {
			return binary, nil
		}
		return filepath.Abs(binary)
	}
	return exec.LookPath(binary)
}

func resolveCommandArgs(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}

	resolved := append([]string(nil), args...)
	for i, arg := range resolved {
		if !isStandaloneRelativePathArg(arg) {
			continue
		}
		abs, err := filepath.Abs(arg)
		if err != nil {
			return nil, err
		}
		resolved[i] = abs
	}
	return resolved, nil
}

func isStandaloneRelativePathArg(arg string) bool {
	if arg == "." || arg == ".." {
		return true
	}
	return strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../")
}

// filterEnv returns a filtered environment excluding sensitive variables and
// variables that interfere with subprocess behaviour.
func filterEnv(env []string) []string {
	blocked := map[string]bool{
		"AWS_SECRET_ACCESS_KEY": true,
		"AWS_SESSION_TOKEN":     true,
		"SLACK_BOT_TOKEN":       true,
		"SLACK_SIGNING_SECRET":  true,
		"DISCORD_TOKEN":         true,
		"CLAUDECODE":            true,
	}
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		key, _, ok := strings.Cut(e, "=")
		if ok && blocked[key] {
			continue
		}
		filtered = append(filtered, e)
	}
	if !hasEnvKey(filtered, "TERM") {
		filtered = append(filtered, "TERM=xterm-256color")
	}
	if !hasEnvKey(filtered, "COLORTERM") {
		filtered = append(filtered, "COLORTERM=truecolor")
	}
	return filtered
}

func hasEnvKey(env []string, key string) bool {
	for _, e := range env {
		k, _, ok := strings.Cut(e, "=")
		if ok && k == key {
			return true
		}
	}
	return false
}
