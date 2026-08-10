package reposetup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
	"gopkg.in/yaml.v3"
)

const envMarker = "__AI_AGENT_BRIDGE_ENV__"

var ErrSetupFailed = errors.New("repo setup failed")

type Options struct {
	Enabled        bool
	ConfigPath     string
	DefaultTimeout time.Duration
	MaxTimeout     time.Duration
	Logger         *slog.Logger
	Redact         func(string) string
	BaseEnv        func() []string
}

type Coordinator struct {
	opts  Options
	group singleflight.Group
}

type fileConfig struct {
	Version int               `yaml:"version"`
	Shell   string            `yaml:"shell"`
	Setup   []string          `yaml:"setup"`
	Env     map[string]string `yaml:"env"`
	Timeout string            `yaml:"timeout"`
}

func NewCoordinator(opts Options) *Coordinator {
	if opts.ConfigPath == "" {
		opts.ConfigPath = ".ai-agent-bridge.yaml"
	}
	if opts.DefaultTimeout <= 0 {
		opts.DefaultTimeout = 2 * time.Minute
	}
	if opts.MaxTimeout <= 0 {
		opts.MaxTimeout = 15 * time.Minute
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Redact == nil {
		opts.Redact = defaultRedact
	}
	if opts.BaseEnv == nil {
		opts.BaseEnv = os.Environ
	}
	return &Coordinator{opts: opts}
}

func (c *Coordinator) Prepare(ctx context.Context, repoPath string, baseEnv []string) ([]string, error) {
	if c == nil {
		if baseEnv == nil {
			baseEnv = os.Environ()
		}
		return append([]string(nil), baseEnv...), nil
	}
	if baseEnv == nil {
		if c.opts.BaseEnv != nil {
			baseEnv = c.opts.BaseEnv()
		} else {
			baseEnv = os.Environ()
		}
	}
	if !c.opts.Enabled {
		return append([]string(nil), baseEnv...), nil
	}
	cleanRepo, configPath, err := resolveConfigPath(repoPath, c.opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return append([]string(nil), baseEnv...), nil
		}
		return nil, fmt.Errorf("%w: stat %s: %v", ErrSetupFailed, configPath, err)
	}

	key := cleanRepo
	env, err, _ := c.group.Do(key, func() (any, error) {
		return c.prepareOnce(ctx, cleanRepo, configPath, baseEnv)
	})
	if err != nil {
		return nil, err
	}
	return append([]string(nil), env.([]string)...), nil
}

func (c *Coordinator) prepareOnce(ctx context.Context, repoPath, configPath string, baseEnv []string) ([]string, error) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSetupFailed, err)
	}
	timeout, err := effectiveTimeout(cfg.Timeout, c.opts.DefaultTimeout, c.opts.MaxTimeout)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSetupFailed, err)
	}
	env := mergeEnv(baseEnv, cfg.Env)

	c.opts.Logger.Info("repo setup starting", "repo_path", repoPath, "config_path", configPath, "timeout", timeout.String(), "shell", cfg.Shell)
	setupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(setupCtx, cfg.Shell, "-lc", setupScript(cfg.Setup))
	cmd.Dir = repoPath
	cmd.Env = env
	setProcessGroup(cmd)

	out, runErr := cmd.CombinedOutput()
	if setupCtx.Err() == context.DeadlineExceeded {
		killProcessGroup(cmd)
		msg := fmt.Sprintf("setup timed out after %s", timeout)
		c.opts.Logger.Warn("repo setup failed", "repo_path", repoPath, "error", msg)
		return nil, fmt.Errorf("%w: %s", ErrSetupFailed, msg)
	}
	if runErr != nil {
		killProcessGroup(cmd)
		msg := strings.TrimSpace(c.opts.Redact(string(out)))
		if msg == "" {
			msg = runErr.Error()
		}
		c.opts.Logger.Warn("repo setup failed", "repo_path", repoPath, "error", runErr, "output", msg)
		return nil, fmt.Errorf("%w: command failed: %v; output: %s", ErrSetupFailed, runErr, msg)
	}

	captured, err := parseCapturedEnv(out)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSetupFailed, err)
	}
	c.opts.Logger.Info("repo setup succeeded", "repo_path", repoPath)
	return captured, nil
}

func loadConfig(path string) (*fileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg := &fileConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("version must be 1")
	}
	if cfg.Shell == "" {
		cfg.Shell = "bash"
	}
	switch cfg.Shell {
	case "bash", "sh":
	default:
		return nil, fmt.Errorf("unsupported shell %q", cfg.Shell)
	}
	for i, line := range cfg.Setup {
		if strings.TrimSpace(line) == "" {
			return nil, fmt.Errorf("setup[%d] must not be empty", i)
		}
	}
	for key := range cfg.Env {
		if !validEnvKey(key) {
			return nil, fmt.Errorf("env key %q is invalid", key)
		}
	}
	return cfg, nil
}

func resolveConfigPath(repoPath, configPath string) (string, string, error) {
	if filepath.IsAbs(configPath) {
		return "", "", fmt.Errorf("%w: config_path must be relative", ErrSetupFailed)
	}
	repoAbs, err := filepath.Abs(repoPath)
	if err != nil {
		return "", "", fmt.Errorf("%w: resolve repo path: %v", ErrSetupFailed, err)
	}
	repoReal, err := filepath.EvalSymlinks(repoAbs)
	if err != nil {
		repoReal = filepath.Clean(repoAbs)
	}
	candidate := filepath.Join(repoReal, configPath)
	cleanCandidate := filepath.Clean(candidate)
	rel, err := filepath.Rel(repoReal, cleanCandidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("%w: config path escapes repo root", ErrSetupFailed)
	}
	return repoReal, cleanCandidate, nil
}

func effectiveTimeout(raw string, def, max time.Duration) (time.Duration, error) {
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("timeout: %w", err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("timeout must be > 0")
	}
	if d > max {
		return 0, fmt.Errorf("timeout %s exceeds max_timeout %s", d, max)
	}
	return d, nil
}

func setupScript(lines []string) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString("printf '\\n")
	b.WriteString(envMarker)
	b.WriteString("\\n'\n")
	b.WriteString("env -0\n")
	return b.String()
}

func parseCapturedEnv(out []byte) ([]string, error) {
	marker := []byte("\n" + envMarker + "\n")
	idx := bytes.LastIndex(out, marker)
	if idx < 0 {
		return nil, fmt.Errorf("environment marker missing from setup output")
	}
	raw := out[idx+len(marker):]
	parts := bytes.Split(raw, []byte{0})
	env := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		if !bytes.Contains(part, []byte("=")) {
			continue
		}
		env = append(env, string(part))
	}
	return env, nil
}

func mergeEnv(base []string, extra map[string]string) []string {
	out := append([]string(nil), base...)
	for key, value := range extra {
		out = setEnv(out, key, value)
	}
	return out
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	item := prefix + value
	for i, existing := range env {
		if strings.HasPrefix(existing, prefix) {
			env[i] = item
			return env
		}
	}
	return append(env, item)
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if i == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func defaultRedact(s string) string {
	for _, key := range []string{"TOKEN", "SECRET", "PASSWORD", "API_KEY"} {
		s = redactAssignments(s, key)
	}
	return s
}

func redactAssignments(s, contains string) string {
	fields := strings.Fields(s)
	for i, field := range fields {
		key, _, ok := strings.Cut(field, "=")
		if ok && strings.Contains(strings.ToUpper(key), contains) {
			fields[i] = key + "=" + strconv.Quote("[REDACTED]")
		}
	}
	if len(fields) == 0 {
		return s
	}
	return strings.Join(fields, " ")
}
