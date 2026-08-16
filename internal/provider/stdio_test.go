package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/markcallen/ai-agent-bridge/internal/bridge"
)

func TestBuildCommandIncludesProviderArgs(t *testing.T) {
	p := NewStdioProvider(StdioConfig{
		ProviderID:    "fake",
		Binary:        "/bin/echo",
		DefaultArgs:   []string{"hello"},
		PromptPattern: "❯",
	})

	cmd, err := p.BuildCommand(context.Background(), bridge.SessionConfig{
		ProjectID: "test",
		SessionID: "session",
		RepoPath:  ".",
		Options: map[string]string{
			"arg:model": "world",
		},
	})
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	if got := p.PromptPattern().String(); got != "❯" {
		t.Fatalf("PromptPattern=%q want=%q", got, "❯")
	}
	if len(cmd.Args) != 3 {
		t.Fatalf("args len=%d want=3 (%v)", len(cmd.Args), cmd.Args)
	}
	if cmd.Args[1] != "hello" || cmd.Args[2] != "world" {
		t.Fatalf("unexpected args: %v", cmd.Args)
	}
}

func TestBuildCommandUsesPreparedEnvironment(t *testing.T) {
	p := NewStdioProvider(StdioConfig{
		ProviderID: "fake",
		Binary:     "/bin/echo",
	})

	cmd, err := p.BuildCommand(context.Background(), bridge.SessionConfig{
		ProjectID: "test",
		SessionID: "session",
		RepoPath:  ".",
		Env:       []string{"FROM_SETUP=1"},
	})
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	if len(cmd.Env) != 1 || cmd.Env[0] != "FROM_SETUP=1" {
		t.Fatalf("Env=%v want prepared environment", cmd.Env)
	}
}

func TestBuildCommandAbsolutizesRelativeScriptArgForNode(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	p := NewStdioProvider(StdioConfig{
		ProviderID:  "claude",
		Binary:      "node",
		DefaultArgs: []string{"./node_modules/@anthropic-ai/claude-code/cli.js", "--verbose"},
	})

	cmd, err := p.BuildCommand(context.Background(), bridge.SessionConfig{
		ProjectID: "test",
		SessionID: "session",
		RepoPath:  "/tmp/other-repo",
	})
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}

	want := filepath.Join(repoRoot, "node_modules/@anthropic-ai/claude-code/cli.js")
	if got := cmd.Args[1]; got != want {
		t.Fatalf("script arg=%q want %q", got, want)
	}
	if got := cmd.Dir; got != "/tmp/other-repo" {
		t.Fatalf("Dir=%q want /tmp/other-repo", got)
	}
}

func TestResolveCommandArgsLeavesFlagsAndURLsUntouched(t *testing.T) {
	args, err := resolveCommandArgs([]string{
		"./node_modules/@anthropic-ai/claude-code/cli.js",
		"--config=./configs/dev.yaml",
		"https://example.com/api",
		"../relative-script.js",
	}, "")
	if err != nil {
		t.Fatalf("resolveCommandArgs: %v", err)
	}

	if !filepath.IsAbs(args[0]) {
		t.Fatalf("first arg should be absolutized, got %q", args[0])
	}
	if got := args[1]; got != "--config=./configs/dev.yaml" {
		t.Fatalf("flag arg=%q", got)
	}
	if got := args[2]; got != "https://example.com/api" {
		t.Fatalf("url arg=%q", got)
	}
	if !filepath.IsAbs(args[3]) {
		t.Fatalf("relative script should be absolutized, got %q", args[3])
	}
}

func TestResolveBinaryPathWithProviderRoot(t *testing.T) {
	root := t.TempDir()

	// Absolute binary is returned as-is regardless of root.
	abs, err := resolveBinaryPath("/usr/bin/node", root)
	if err != nil {
		t.Fatalf("resolveBinaryPath absolute: %v", err)
	}
	if abs != "/usr/bin/node" {
		t.Fatalf("absolute path changed: %q", abs)
	}

	// NAME-only binary (no slash) is still looked up on PATH.
	_, err = resolveBinaryPath("cat", root)
	if err != nil {
		t.Fatalf("resolveBinaryPath PATH lookup: %v", err)
	}

	// Relative path with slash resolves against root, not CWD.
	got, err := resolveBinaryPath("./bin/tool", root)
	if err != nil {
		t.Fatalf("resolveBinaryPath relative: %v", err)
	}
	want := filepath.Join(root, "./bin/tool")
	if got != want {
		t.Fatalf("resolveBinaryPath=%q want %q", got, want)
	}
}

func TestResolveCommandArgsWithProviderRoot(t *testing.T) {
	root := t.TempDir()

	args, err := resolveCommandArgs([]string{
		"./node_modules/@openai/codex/bin/codex.js",
		"--flag",
		"../sibling.js",
	}, root)
	if err != nil {
		t.Fatalf("resolveCommandArgs: %v", err)
	}

	// Relative paths with slashes are resolved against root.
	wantFirst := filepath.Join(root, "./node_modules/@openai/codex/bin/codex.js")
	if args[0] != wantFirst {
		t.Fatalf("args[0]=%q want %q", args[0], wantFirst)
	}
	// Non-path flags are left alone.
	if args[1] != "--flag" {
		t.Fatalf("args[1]=%q want --flag", args[1])
	}
	wantThird := filepath.Join(root, "../sibling.js")
	if args[2] != wantThird {
		t.Fatalf("args[2]=%q want %q", args[2], wantThird)
	}
}

func TestBuildCommandResolvesArgsFromProviderRoot(t *testing.T) {
	root := t.TempDir()

	p := NewStdioProvider(StdioConfig{
		ProviderID:    "codex",
		Binary:        "/usr/bin/node",
		DefaultArgs:   []string{"./node_modules/@openai/codex/bin/codex.js"},
		PromptPattern: "›",
		ProviderRoot:  root,
	})

	cmd, err := p.BuildCommand(context.Background(), bridge.SessionConfig{
		ProjectID: "test",
		SessionID: "s1",
		RepoPath:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}

	want := filepath.Join(root, "./node_modules/@openai/codex/bin/codex.js")
	if len(cmd.Args) < 2 || cmd.Args[1] != want {
		t.Fatalf("cmd.Args=%v want second arg %q", cmd.Args, want)
	}
}

func TestBuildCommandAppendsProviderScopedUnprotectedArgs(t *testing.T) {
	tests := []struct {
		provider string
		envVar   string
		wantArgs []string
	}{
		{
			provider: "codex",
			envVar:   "BRIDGE_CODEX_UNPROTECTED",
			wantArgs: []string{"--dangerously-bypass-approvals-and-sandbox"},
		},
		{
			provider: "claude",
			envVar:   "BRIDGE_CLAUDE_UNPROTECTED",
			wantArgs: []string{"--dangerously-skip-permissions"},
		},
		{
			provider: "opencode",
			envVar:   "BRIDGE_OPENCODE_UNPROTECTED",
			wantArgs: []string{"--auto"},
		},
		{
			provider: "gemini",
			envVar:   "BRIDGE_GEMINI_UNPROTECTED",
			wantArgs: []string{"--yolo"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			clearUnprotectedEnv(t)
			t.Setenv(tc.envVar, "true")

			p := NewStdioProvider(StdioConfig{
				ProviderID:  tc.provider,
				Binary:      "/bin/echo",
				DefaultArgs: []string{"base"},
			})

			cmd, err := p.BuildCommand(context.Background(), bridge.SessionConfig{
				ProjectID: "test",
				SessionID: "session",
				RepoPath:  ".",
			})
			if err != nil {
				t.Fatalf("BuildCommand: %v", err)
			}
			for _, wantArg := range tc.wantArgs {
				if !containsString(cmd.Args, wantArg) {
					t.Fatalf("cmd.Args=%v missing %q", cmd.Args, wantArg)
				}
				if got := countString(cmd.Args, wantArg); got != 1 {
					t.Fatalf("%q count=%d want 1 in %v", wantArg, got, cmd.Args)
				}
			}
		})
	}
}

func TestBuildCommandUnprotectedEnvIsProviderScoped(t *testing.T) {
	clearUnprotectedEnv(t)
	t.Setenv("BRIDGE_CODEX_UNPROTECTED", "true")

	p := NewStdioProvider(StdioConfig{
		ProviderID:  "claude",
		Binary:      "/bin/echo",
		DefaultArgs: []string{"base"},
	})

	cmd, err := p.BuildCommand(context.Background(), bridge.SessionConfig{
		ProjectID: "test",
		SessionID: "session",
		RepoPath:  ".",
	})
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	if containsString(cmd.Args, "--dangerously-skip-permissions") {
		t.Fatalf("claude args unexpectedly enabled by codex env: %v", cmd.Args)
	}
}

func TestBuildCommandUnprotectedFalseDoesNotAppendArgs(t *testing.T) {
	clearUnprotectedEnv(t)
	t.Setenv("BRIDGE_CODEX_UNPROTECTED", "false")

	p := NewStdioProvider(StdioConfig{
		ProviderID:  "codex",
		Binary:      "/bin/echo",
		DefaultArgs: []string{"base"},
	})

	cmd, err := p.BuildCommand(context.Background(), bridge.SessionConfig{
		ProjectID: "test",
		SessionID: "session",
		RepoPath:  ".",
	})
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	if containsString(cmd.Args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("unprotected args appended with false env: %v", cmd.Args)
	}
}

func TestBuildCommandInvalidUnprotectedEnvFailsClosed(t *testing.T) {
	clearUnprotectedEnv(t)
	t.Setenv("BRIDGE_CODEX_UNPROTECTED", "maybe")

	p := NewStdioProvider(StdioConfig{
		ProviderID:  "codex",
		Binary:      "/bin/echo",
		DefaultArgs: []string{"base"},
	})

	_, err := p.BuildCommand(context.Background(), bridge.SessionConfig{
		ProjectID: "test",
		SessionID: "session",
		RepoPath:  ".",
	})
	if err == nil {
		t.Fatal("expected invalid unprotected env error")
	}
	if !strings.Contains(err.Error(), "BRIDGE_CODEX_UNPROTECTED") {
		t.Fatalf("error %q should mention env var", err)
	}
}

func TestHealthWithEnvInvalidUnprotectedEnvFailsClosed(t *testing.T) {
	clearUnprotectedEnv(t)

	p := NewStdioProvider(StdioConfig{
		ProviderID: "codex",
		Binary:     "/bin/echo",
	})

	err := p.HealthWithEnv(context.Background(), []string{"BRIDGE_CODEX_UNPROTECTED=maybe"})
	if err == nil {
		t.Fatal("expected invalid unprotected env error")
	}
	if !strings.Contains(err.Error(), "BRIDGE_CODEX_UNPROTECTED") {
		t.Fatalf("error %q should mention env var", err)
	}
}

func TestProbeArgsUseProviderScopedUnprotectedMode(t *testing.T) {
	clearUnprotectedEnv(t)
	t.Setenv("BRIDGE_CLAUDE_UNPROTECTED", "1")

	got, err := commandArgsForProvider("claude", []string{"--verbose"}, "")
	if err != nil {
		t.Fatalf("commandArgsForProvider: %v", err)
	}
	if !containsString(got, "--dangerously-skip-permissions") {
		t.Fatalf("probe args missing claude unprotected flag: %v", got)
	}
}

func clearUnprotectedEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"BRIDGE_CODEX_UNPROTECTED",
		"BRIDGE_CLAUDE_UNPROTECTED",
		"BRIDGE_OPENCODE_UNPROTECTED",
		"BRIDGE_GEMINI_UNPROTECTED",
	} {
		t.Setenv(key, "")
		_ = os.Unsetenv(key)
	}
}

func countString(values []string, target string) int {
	count := 0
	for _, value := range values {
		if value == target {
			count++
		}
	}
	return count
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
