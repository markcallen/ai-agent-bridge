package bridgelib

import (
	"context"
	"testing"
	"time"

	"github.com/markcallen/ai-agent-bridge/internal/bridge"
)

// newTestBridge returns a bridge with a minimal valid config using the default
// claude provider.  Because no real agent binary is launched, tests that call
// StartSession are expected to fail at the process-exec level, but all of the
// bridge construction and API surface can still be exercised.
func newTestBridge(t *testing.T) *Bridge {
	t.Helper()
	b, err := New(Config{
		Providers: []ProviderConfig{
			{
				ID:             "claude",
				Binary:         "false", // always exits 1; no real agent
				Args:           []string{},
				StartupTimeout: 50 * time.Millisecond,
				StopGrace:      50 * time.Millisecond,
			},
		},
		MaxSessions:           5,
		MaxSessionsPerProject: 2,
		EventBufferSize:       64,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(b.Close)
	return b
}

// TestNew_DefaultConfig verifies that an empty Providers list causes New to
// insert the default "claude" provider automatically.
func TestNew_DefaultConfig(t *testing.T) {
	// Empty Providers → default "claude" provider is registered.
	// We override the binary to something that doesn't need to be installed.
	b, err := New(Config{})
	if err != nil {
		t.Fatalf("New with empty config: %v", err)
	}
	defer b.Close()
	providers := b.ListProviders()
	found := false
	for _, p := range providers {
		if p == "claude" {
			found = true
		}
	}
	if !found {
		t.Fatalf("default provider 'claude' not registered; got %v", providers)
	}
}

// TestNew_MultiProvider ensures multiple providers can be registered.
func TestNew_MultiProvider(t *testing.T) {
	b, err := New(Config{
		Providers: []ProviderConfig{
			{
				ID:             "p1",
				Binary:         "false",
				Args:           []string{},
				StartupTimeout: 50 * time.Millisecond,
				StopGrace:      50 * time.Millisecond,
			},
			{
				ID:             "p2",
				Binary:         "false",
				Args:           []string{},
				StartupTimeout: 50 * time.Millisecond,
				StopGrace:      50 * time.Millisecond,
			},
		},
	})
	if err != nil {
		t.Fatalf("New with multi provider: %v", err)
	}
	defer b.Close()

	providers := b.ListProviders()
	if len(providers) != 2 {
		t.Fatalf("ListProviders got %d, want 2", len(providers))
	}
}

// TestNew_DuplicateProvider ensures duplicate provider IDs are rejected.
func TestNew_DuplicateProvider(t *testing.T) {
	_, err := New(Config{
		Providers: []ProviderConfig{
			{ID: "dup", Binary: "false", StartupTimeout: 50 * time.Millisecond, StopGrace: 50 * time.Millisecond},
			{ID: "dup", Binary: "false", StartupTimeout: 50 * time.Millisecond, StopGrace: 50 * time.Millisecond},
		},
	})
	if err == nil {
		t.Fatal("expected error for duplicate provider ID, got nil")
	}
}

// TestListProviders verifies the registered providers are returned.
func TestListProviders(t *testing.T) {
	b := newTestBridge(t)
	providers := b.ListProviders()
	if len(providers) == 0 {
		t.Fatal("expected at least one provider")
	}
	found := false
	for _, p := range providers {
		if p == "claude" {
			found = true
		}
	}
	if !found {
		t.Fatalf("provider 'claude' not found in %v", providers)
	}
}

// TestList_Empty verifies List returns an empty slice when no sessions exist.
func TestList_Empty(t *testing.T) {
	b := newTestBridge(t)
	sessions := b.List("")
	if len(sessions) != 0 {
		t.Fatalf("List got %d sessions, want 0", len(sessions))
	}
}

// TestGet_NotFound verifies Get returns an error for an unknown session.
func TestGet_NotFound(t *testing.T) {
	b := newTestBridge(t)
	_, err := b.Get("nonexistent-session")
	if err == nil {
		t.Fatal("expected error for unknown session, got nil")
	}
}

// TestSend_NotFound verifies Send returns an error for an unknown session.
func TestSend_NotFound(t *testing.T) {
	b := newTestBridge(t)
	_, err := b.Send("nonexistent-session", "hello")
	if err == nil {
		t.Fatal("expected error for unknown session, got nil")
	}
}

// TestStop_NotFound verifies Stop returns an error for an unknown session.
func TestStop_NotFound(t *testing.T) {
	b := newTestBridge(t)
	err := b.Stop("nonexistent-session", false)
	if err == nil {
		t.Fatal("expected error for unknown session, got nil")
	}
}

// TestStreamEvents_NotFound verifies StreamEvents returns an error for an
// unknown session.
func TestStreamEvents_NotFound(t *testing.T) {
	b := newTestBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := b.StreamEvents(ctx, "nonexistent-session", "sub1", 0)
	if err == nil {
		t.Fatal("expected error for unknown session, got nil")
	}
}

// TestHealth_Returns verifies Health does not panic and returns a map.
func TestHealth_Returns(t *testing.T) {
	b := newTestBridge(t)
	results := b.Health(context.Background())
	if results == nil {
		t.Fatal("Health returned nil map")
	}
	// "false" binary exits immediately, so the provider may report unhealthy —
	// that's fine; we just verify the call works.
}

// TestEventTypeName covers eventTypeName for every known proto EventType value.
func TestEventTypeName(t *testing.T) {
	cases := []struct {
		input    bridge.EventType
		wantName string
	}{
		{bridge.EventType(0), "unspecified"},
		{bridge.EventTypeSessionStarted, "session_started"},
		{bridge.EventTypeSessionStopped, "session_stopped"},
		{bridge.EventTypeSessionFailed, "session_failed"},
		{bridge.EventTypeStdout, "stdout"},
		{bridge.EventTypeStderr, "stderr"},
		{bridge.EventTypeInputReceived, "input_received"},
		{bridge.EventTypeBufferOverflow, "buffer_overflow"},
		{bridge.EventTypeAgentReady, "agent_ready"},
		{bridge.EventTypeResponseComplete, "response_complete"},
		{bridge.EventType(99), "unspecified"},
	}
	for _, tc := range cases {
		got := eventTypeName(tc.input)
		if got != tc.wantName {
			t.Errorf("eventTypeName(%d) = %q, want %q", tc.input, got, tc.wantName)
		}
	}
}
