package bridge

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// mockProvider implements Provider for testing.
type mockProvider struct {
	id string
}

func newMockProvider(id string) *mockProvider {
	return &mockProvider{id: id}
}

func (m *mockProvider) ID() string                                   { return m.id }
func (m *mockProvider) Health(ctx context.Context) error             { return nil }
func (m *mockProvider) Send(handle SessionHandle, text string) error { return nil }

func (m *mockProvider) Events(handle SessionHandle) <-chan Event {
	h := handle.(*mockHandle)
	return h.events
}

func (m *mockProvider) Start(ctx context.Context, cfg SessionConfig) (SessionHandle, error) {
	h := &mockHandle{id: cfg.SessionID, events: make(chan Event, 64)}
	h.events <- Event{
		Type:      EventTypeSessionStarted,
		Stream:    "system",
		Text:      "started",
		Timestamp: time.Now(),
		SessionID: cfg.SessionID,
		ProjectID: cfg.ProjectID,
		Provider:  m.id,
	}
	return h, nil
}

func (m *mockProvider) Stop(handle SessionHandle) error {
	h := handle.(*mockHandle)
	h.events <- Event{
		Type:   EventTypeSessionStopped,
		Stream: "system",
		Text:   "stopped",
		Done:   true,
	}
	close(h.events)
	return nil
}

type mockHandle struct {
	id     string
	events chan Event
}

func (h *mockHandle) ID() string { return h.id }
func (h *mockHandle) PID() int   { return 0 }

func TestSupervisorStartGetStop(t *testing.T) {
	reg := NewRegistry()
	mp := newMockProvider("test")
	reg.Register(mp)

	sup := NewSupervisor(reg, DefaultPolicy(), 100, DefaultSubscriberConfig())
	defer sup.Close()

	info, err := sup.Start(context.Background(), SessionConfig{
		SessionID: "s1",
		ProjectID: "p1",
		RepoPath:  "/tmp",
		Options:   map[string]string{"provider": "test"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if info.SessionID != "s1" {
		t.Errorf("SessionID = %q, want %q", info.SessionID, "s1")
	}
	if info.State != SessionStateRunning {
		t.Errorf("State = %d, want Running", info.State)
	}

	// Get
	got, err := sup.Get("s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ProjectID != "p1" {
		t.Errorf("ProjectID = %q, want %q", got.ProjectID, "p1")
	}

	// List
	list := sup.List("p1")
	if len(list) != 1 {
		t.Errorf("List(p1) = %d, want 1", len(list))
	}

	// Stop
	if err := sup.Stop("s1", false); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Wait for state update
	time.Sleep(50 * time.Millisecond)

	got, err = sup.Get("s1")
	if err != nil {
		t.Fatalf("Get after stop: %v", err)
	}
	if got.State != SessionStateStopped {
		t.Errorf("State after stop = %d, want Stopped", got.State)
	}
}

func TestSupervisorDuplicateSession(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newMockProvider("test"))
	sup := NewSupervisor(reg, DefaultPolicy(), 100, DefaultSubscriberConfig())
	defer sup.Close()

	_, err := sup.Start(context.Background(), SessionConfig{
		SessionID: "dup",
		ProjectID: "p1",
		RepoPath:  "/tmp",
		Options:   map[string]string{"provider": "test"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	_, err = sup.Start(context.Background(), SessionConfig{
		SessionID: "dup",
		ProjectID: "p1",
		RepoPath:  "/tmp",
		Options:   map[string]string{"provider": "test"},
	})
	if err == nil {
		t.Error("expected error for duplicate session")
	}
}

func TestSupervisorSessionLimits(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newMockProvider("test"))

	policy := DefaultPolicy()
	policy.MaxPerProject = 2
	policy.MaxGlobal = 3

	sup := NewSupervisor(reg, policy, 100, DefaultSubscriberConfig())
	defer sup.Close()

	for i := 0; i < 2; i++ {
		_, err := sup.Start(context.Background(), SessionConfig{
			SessionID: fmt.Sprintf("s%d", i),
			ProjectID: "p1",
			RepoPath:  "/tmp",
			Options:   map[string]string{"provider": "test"},
		})
		if err != nil {
			t.Fatalf("Start s%d: %v", i, err)
		}
	}

	// Should fail: per-project limit
	_, err := sup.Start(context.Background(), SessionConfig{
		SessionID: "s-extra",
		ProjectID: "p1",
		RepoPath:  "/tmp",
		Options:   map[string]string{"provider": "test"},
	})
	if err == nil {
		t.Error("expected per-project limit error")
	}

	// Different project should work (global limit not hit)
	_, err = sup.Start(context.Background(), SessionConfig{
		SessionID: "s-other",
		ProjectID: "p2",
		RepoPath:  "/tmp",
		Options:   map[string]string{"provider": "test"},
	})
	if err != nil {
		t.Fatalf("Start for p2: %v", err)
	}

	// Should fail: global limit (3 total)
	_, err = sup.Start(context.Background(), SessionConfig{
		SessionID: "s-global",
		ProjectID: "p3",
		RepoPath:  "/tmp",
		Options:   map[string]string{"provider": "test"},
	})
	if err == nil {
		t.Error("expected global limit error")
	}
}

func TestSupervisorEventBuffer(t *testing.T) {
	reg := NewRegistry()
	reg.Register(newMockProvider("test"))
	sup := NewSupervisor(reg, DefaultPolicy(), 100, DefaultSubscriberConfig())
	defer sup.Close()

	_, err := sup.Start(context.Background(), SessionConfig{
		SessionID: "ev1",
		ProjectID: "p1",
		RepoPath:  "/tmp",
		Options:   map[string]string{"provider": "test"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Give event forwarding goroutine time to process
	time.Sleep(50 * time.Millisecond)

	buf, err := sup.EventBuffer("ev1")
	if err != nil {
		t.Fatalf("EventBuffer: %v", err)
	}

	if buf.Len() == 0 {
		t.Error("expected at least one event in buffer")
	}

	events := buf.After(0)
	if events[0].Type != EventTypeSessionStarted {
		t.Errorf("first event type = %d, want SessionStarted", events[0].Type)
	}
}
