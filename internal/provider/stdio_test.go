package provider

import (
	"context"
	"os"
	"path/filepath"
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
