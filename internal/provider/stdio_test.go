package provider

import (
	"context"
	"os"
	"path/filepath"
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
	})
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
