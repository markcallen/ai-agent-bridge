package provider

import (
	"context"
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
