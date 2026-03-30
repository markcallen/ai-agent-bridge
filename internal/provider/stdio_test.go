package provider

import (
	"context"
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
	if cmd.SysProcAttr != nil {
		t.Fatalf("SysProcAttr=%#v want nil for PTY launch commands", cmd.SysProcAttr)
	}
}
