package provider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/markcallen/ai-agent-bridge/internal/bridge"
)

func TestStdioProviderEcho(t *testing.T) {
	p := NewStdioProvider(StdioConfig{
		ProviderID:     "test-echo",
		Binary:         "echo",
		DefaultArgs:    []string{"hello from echo"},
		StartupTimeout: 5 * time.Second,
		StopGrace:      2 * time.Second,
	})

	if p.ID() != "test-echo" {
		t.Errorf("ID = %q, want %q", p.ID(), "test-echo")
	}

	handle, err := p.Start(context.Background(), bridge.SessionConfig{
		ProjectID: "test-project",
		SessionID: "test-session",
		RepoPath:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if handle.PID() <= 0 {
		t.Errorf("PID = %d, want > 0", handle.PID())
	}

	events := p.Events(handle)

	// Collect events with timeout
	var collected []bridge.Event
	timeout := time.After(5 * time.Second)
loop:
	for {
		select {
		case e, ok := <-events:
			if !ok {
				break loop
			}
			collected = append(collected, e)
			if e.Done {
				break loop
			}
		case <-timeout:
			t.Fatal("timeout waiting for events")
		}
	}

	if len(collected) < 2 {
		t.Fatalf("got %d events, want at least 2 (started + output/stopped)", len(collected))
	}

	// First event should be session started
	if collected[0].Type != bridge.EventTypeSessionStarted {
		t.Errorf("first event type = %d, want SessionStarted", collected[0].Type)
	}

	// Should have stdout with "hello from echo"
	foundHello := false
	for _, e := range collected {
		if e.Type == bridge.EventTypeStdout && e.Text == "hello from echo" {
			foundHello = true
		}
	}
	if !foundHello {
		t.Errorf("did not find stdout event with 'hello from echo'")
	}
}

func TestStdioProviderCat(t *testing.T) {
	p := NewStdioProvider(StdioConfig{
		ProviderID:     "test-cat",
		Binary:         "cat",
		DefaultArgs:    nil,
		StartupTimeout: 5 * time.Second,
		StopGrace:      2 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle, err := p.Start(ctx, bridge.SessionConfig{
		ProjectID: "test-project",
		SessionID: "test-cat-session",
		RepoPath:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	events := p.Events(handle)

	// Wait for started event
	select {
	case e := <-events:
		if e.Type != bridge.EventTypeSessionStarted {
			t.Errorf("first event type = %d, want SessionStarted", e.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for started event")
	}

	// Send input
	if err := p.Send(handle, "test input"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Should get echo back from cat
	select {
	case e := <-events:
		if e.Type != bridge.EventTypeStdout || e.Text != "test input" {
			t.Errorf("got event type=%d text=%q, want stdout with 'test input'", e.Type, e.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for echo event")
	}

	// Stop the session
	if err := p.Stop(handle); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestStdioProviderHealth(t *testing.T) {
	p := NewStdioProvider(StdioConfig{
		ProviderID: "test-health",
		Binary:     "echo",
	})

	if err := p.Health(context.Background()); err != nil {
		t.Errorf("Health for 'echo': %v", err)
	}

	bad := NewStdioProvider(StdioConfig{
		ProviderID: "bad",
		Binary:     "nonexistent-binary-xyz",
	})

	if err := bad.Health(context.Background()); err == nil {
		t.Error("Health for nonexistent binary should fail")
	}
}

func TestStdioProviderStartTimeout(t *testing.T) {
	p := NewStdioProvider(StdioConfig{
		ProviderID:     "timeout",
		Binary:         "echo",
		DefaultArgs:    []string{"hello"},
		StartupTimeout: 10 * time.Millisecond,
	})
	p.starter = func(cmd *exec.Cmd) error {
		time.Sleep(200 * time.Millisecond)
		return fmt.Errorf("delayed start")
	}

	_, err := p.Start(context.Background(), bridge.SessionConfig{
		ProjectID: "test-project",
		SessionID: "timeout-session",
		RepoPath:  t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected startup timeout error")
	}
	if !strings.Contains(err.Error(), "startup timeout") {
		t.Fatalf("expected startup timeout error, got: %v", err)
	}
}

func TestStreamJSONParsing(t *testing.T) {
	// Create a shell script that outputs Claude Code CLI stream-json lines.
	// The real format is NDJSON where "assistant" events carry model output.
	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "stream-json-provider.sh")
	script := `#!/usr/bin/env sh
echo '{"type":"system","subtype":"init","session_id":"test-session","tools":[],"mcp_servers":[]}'
echo '{"type":"assistant","message":{"id":"msg_01","type":"message","role":"assistant","content":[{"type":"text","text":"Hello"}],"model":"claude-opus-4-6","stop_reason":"end_turn"},"session_id":"test-session","parent_tool_use_id":null}'
echo '{"type":"assistant","message":{"id":"msg_02","type":"message","role":"assistant","content":[{"type":"text","text":" world"}],"model":"claude-opus-4-6","stop_reason":"end_turn"},"session_id":"test-session","parent_tool_use_id":null}'
echo '{"type":"result","subtype":"success","result":"Hello world","session_id":"test-session","duration_ms":100,"total_cost_usd":0.001}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	p := NewStdioProvider(StdioConfig{
		ProviderID:     "test-stream-json",
		Binary:         scriptPath,
		StartupTimeout: 5 * time.Second,
		StopGrace:      2 * time.Second,
		StreamJSON:     true,
	})

	handle, err := p.Start(context.Background(), bridge.SessionConfig{
		ProjectID: "test-project",
		SessionID: "test-stream-json-session",
		RepoPath:  tmp,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	events := p.Events(handle)
	var texts []string
	var gotAgentReady, gotResponseComplete bool
	timeout := time.After(5 * time.Second)
loop:
	for {
		select {
		case e, ok := <-events:
			if !ok {
				break loop
			}
			switch e.Type {
			case bridge.EventTypeStdout:
				texts = append(texts, e.Text)
			case bridge.EventTypeAgentReady:
				gotAgentReady = true
			case bridge.EventTypeResponseComplete:
				gotResponseComplete = true
			}
			if e.Done {
				break loop
			}
		case <-timeout:
			t.Fatal("timeout waiting for events")
		}
	}

	// Should only get text events from "assistant" events, not system/result etc.
	if len(texts) != 2 {
		t.Fatalf("got %d text events %v, want 2", len(texts), texts)
	}
	if texts[0] != "Hello" {
		t.Errorf("texts[0] = %q, want %q", texts[0], "Hello")
	}
	if texts[1] != " world" {
		t.Errorf("texts[1] = %q, want %q", texts[1], " world")
	}
	if !gotAgentReady {
		t.Error("expected AGENT_READY event, got none")
	}
	if !gotResponseComplete {
		t.Error("expected RESPONSE_COMPLETE event from result event, got none")
	}
}

func TestPromptPatternDetection(t *testing.T) {
	// Simulates a PTY-based REPL: prompt → output → prompt → output → prompt.
	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "repl.sh")
	script := `#!/usr/bin/env sh
echo '> '
echo 'first response line'
echo '> '
echo 'second response line'
echo '> '
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	p := NewStdioProvider(StdioConfig{
		ProviderID:     "test-prompt",
		Binary:         scriptPath,
		StartupTimeout: 5 * time.Second,
		StopGrace:      2 * time.Second,
		PromptPattern:  `^>\s*$`,
	})

	handle, err := p.Start(context.Background(), bridge.SessionConfig{
		ProjectID: "test-project",
		SessionID: "test-prompt-session",
		RepoPath:  tmp,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	events := p.Events(handle)
	var texts []string
	var gotAgentReady int
	var gotResponseComplete int
	timeout := time.After(5 * time.Second)
loop:
	for {
		select {
		case e, ok := <-events:
			if !ok {
				break loop
			}
			switch e.Type {
			case bridge.EventTypeStdout:
				texts = append(texts, e.Text)
			case bridge.EventTypeAgentReady:
				gotAgentReady++
			case bridge.EventTypeResponseComplete:
				gotResponseComplete++
			}
			if e.Done {
				break loop
			}
		case <-timeout:
			t.Fatal("timeout waiting for events")
		}
	}

	if gotAgentReady != 1 {
		t.Errorf("got %d AGENT_READY events, want 1", gotAgentReady)
	}
	if gotResponseComplete != 2 {
		t.Errorf("got %d RESPONSE_COMPLETE events, want 2", gotResponseComplete)
	}
	if len(texts) != 2 {
		t.Errorf("got %d stdout lines %v, want 2", len(texts), texts)
	}
}

func TestStdioProviderVersion(t *testing.T) {
	t.Run("version_script", func(t *testing.T) {
		// A fake binary that prints a version string to stdout with --version.
		tmp := t.TempDir()
		scriptPath := filepath.Join(tmp, "fake-provider")
		if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env sh\necho 'fake-provider 2.3.1'\n"), 0o755); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		p := NewStdioProvider(StdioConfig{
			ProviderID: "fake",
			Binary:     scriptPath,
		})
		v, err := p.Version(context.Background())
		if err != nil {
			t.Fatalf("Version: %v", err)
		}
		if v != "fake-provider 2.3.1" {
			t.Errorf("Version = %q, want %q", v, "fake-provider 2.3.1")
		}
	})

	t.Run("missing_binary", func(t *testing.T) {
		p := NewStdioProvider(StdioConfig{
			ProviderID: "missing",
			Binary:     "nonexistent-binary-xyz-version",
		})
		_, err := p.Version(context.Background())
		if err == nil {
			t.Error("expected error for missing binary")
		}
	})
}

func TestClaudeProviderStreamJSON(t *testing.T) {
	// Build a fake "claude" binary that emits mock stream-json output.
	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "claude")
	script := `#!/usr/bin/env sh
echo '{"type":"system","subtype":"init","session_id":"test-session","tools":[],"mcp_servers":[]}'
echo '{"type":"assistant","message":{"id":"msg_01","type":"message","role":"assistant","content":[{"type":"text","text":"Hi there"}],"model":"claude-opus-4-6","stop_reason":"end_turn"},"session_id":"test-session","parent_tool_use_id":null}'
echo '{"type":"result","subtype":"success","result":"Hi there","session_id":"test-session","duration_ms":50,"total_cost_usd":0.001}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Construct the provider the same way NewClaudeProvider() does, but with our fake binary.
	p := NewStdioProvider(StdioConfig{
		ProviderID:     "claude",
		Binary:         scriptPath,
		DefaultArgs:    []string{"--output-format", "stream-json", "--verbose"},
		StreamJSON:     true,
		StartupTimeout: 5 * time.Second,
		StopGrace:      2 * time.Second,
	})

	handle, err := p.Start(context.Background(), bridge.SessionConfig{
		ProjectID: "test-project",
		SessionID: "claude-stream-json-session",
		RepoPath:  tmp,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	events := p.Events(handle)
	var gotAgentReady, gotResponseComplete bool
	var stdoutTexts []string
	timeout := time.After(5 * time.Second)
loop:
	for {
		select {
		case e, ok := <-events:
			if !ok {
				break loop
			}
			switch e.Type {
			case bridge.EventTypeAgentReady:
				gotAgentReady = true
			case bridge.EventTypeStdout:
				stdoutTexts = append(stdoutTexts, e.Text)
			case bridge.EventTypeResponseComplete:
				gotResponseComplete = true
			}
			if e.Done {
				break loop
			}
		case <-timeout:
			t.Fatal("timeout waiting for events")
		}
	}

	if !gotAgentReady {
		t.Error("expected AGENT_READY event")
	}
	if len(stdoutTexts) == 0 {
		t.Error("expected at least one STDOUT event with assistant text")
	} else if stdoutTexts[0] != "Hi there" {
		t.Errorf("stdout[0] = %q, want %q", stdoutTexts[0], "Hi there")
	}
	if !gotResponseComplete {
		t.Error("expected RESPONSE_COMPLETE event")
	}
}

func TestResolveBinaryPathRelativeSlash(t *testing.T) {
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})

	tmp := t.TempDir()
	scriptsDir := filepath.Join(tmp, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	scriptPath := filepath.Join(scriptsDir, "echo-test.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env sh\necho resolved\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir tmp: %v", err)
	}

	resolved, err := resolveBinaryPath("./scripts/echo-test.sh")
	if err != nil {
		t.Fatalf("resolveBinaryPath: %v", err)
	}
	if !filepath.IsAbs(resolved) {
		t.Fatalf("resolved path is not absolute: %q", resolved)
	}

	otherDir := t.TempDir()
	p := NewStdioProvider(StdioConfig{
		ProviderID: "relative-binary",
		Binary:     "./scripts/echo-test.sh",
		StopGrace:  2 * time.Second,
	})

	handle, err := p.Start(context.Background(), bridge.SessionConfig{
		ProjectID: "test-project",
		SessionID: "test-session",
		RepoPath:  otherDir,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	events := p.Events(handle)
	timeout := time.After(5 * time.Second)
	foundStdout := false
	for {
		select {
		case e, ok := <-events:
			if !ok {
				if !foundStdout {
					t.Fatal("did not receive expected stdout event")
				}
				return
			}
			if e.Type == bridge.EventTypeStdout && e.Text == "resolved" {
				foundStdout = true
			}
			if e.Done {
				if !foundStdout {
					t.Fatal("session ended before expected stdout event")
				}
				return
			}
		case <-timeout:
			t.Fatal("timeout waiting for events")
		}
	}
}
