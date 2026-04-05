package bridge

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"regexp"
	"testing"
	"time"
)

type testProvider struct {
	id        string
	healthErr error
}

func (p *testProvider) ID() string                            { return p.id }
func (p *testProvider) Binary() string                        { return "/bin/cat" }
func (p *testProvider) PromptPattern() *regexp.Regexp         { return nil }
func (p *testProvider) StartupTimeout() time.Duration         { return time.Second }
func (p *testProvider) StopGrace() time.Duration              { return 50 * time.Millisecond }
func (p *testProvider) ValidateStartup(context.Context) error { return nil }
func (p *testProvider) Health(context.Context) error          { return p.healthErr }
func (p *testProvider) Version(context.Context) (string, error) {
	return "test-provider", nil
}
func (p *testProvider) BuildCommand(ctx context.Context, cfg SessionConfig) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "/bin/cat")
	cmd.Dir = cfg.RepoPath
	return cmd, nil
}

func TestSupervisorSessionLifecycle(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&testProvider{id: "fake"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	supervisor := NewSupervisor(registry, DefaultPolicy(), 1024, time.Minute)
	defer supervisor.Close()

	info, err := supervisor.Start(context.Background(), SessionConfig{
		ProjectID:   "project-a",
		SessionID:   "session-a",
		RepoPath:    t.TempDir(),
		Options:     map[string]string{"provider": "fake"},
		InitialCols: 80,
		InitialRows: 24,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if info.Provider != "fake" {
		t.Fatalf("Provider=%q want %q", info.Provider, "fake")
	}

	state, err := supervisor.Attach("session-a", "client-a", 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if _, err := supervisor.Attach("session-a", "client-b", 0); !errors.Is(err, ErrSessionAlreadyAttached) {
		t.Fatalf("Attach while attached error=%v want %v", err, ErrSessionAlreadyAttached)
	}

	if _, err := supervisor.WriteInput("session-a", "wrong-client", []byte("hello\n")); !errors.Is(err, ErrClientMismatch) {
		t.Fatalf("WriteInput wrong client error=%v want %v", err, ErrClientMismatch)
	}
	if err := supervisor.Resize("session-a", "wrong-client", 100, 40); !errors.Is(err, ErrClientMismatch) {
		t.Fatalf("Resize wrong client error=%v want %v", err, ErrClientMismatch)
	}

	if _, err := supervisor.WriteInput("session-a", "client-a", []byte("hello\n")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	chunk := waitForChunk(t, state.Live, "hello")
	if !bytes.Contains(chunk.Payload, []byte("hello")) {
		t.Fatalf("chunk payload=%q does not contain hello", string(chunk.Payload))
	}

	if err := supervisor.Resize("session-a", "client-a", 100, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	got, err := supervisor.Get("session-a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Cols != 100 || got.Rows != 40 {
		t.Fatalf("size=%dx%d want 100x40", got.Cols, got.Rows)
	}

	if err := supervisor.Detach("session-a", "client-a"); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	replayState, err := supervisor.Attach("session-a", "client-b", 0)
	if err != nil {
		t.Fatalf("Attach replay: %v", err)
	}
	if len(replayState.Replay) == 0 {
		t.Fatal("Replay was empty, want buffered output")
	}
	if err := supervisor.Detach("session-a", "client-b"); err != nil {
		t.Fatalf("Detach replay client: %v", err)
	}

	items := supervisor.List("project-a")
	if len(items) != 1 {
		t.Fatalf("List len=%d want 1", len(items))
	}

	if err := supervisor.Stop("session-a", true); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitForStopped(t, supervisor, "session-a")
}

func TestSupervisorStartValidationAndLimits(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&testProvider{id: "fake"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.Register(&testProvider{id: "bad", healthErr: errors.New("down")}); err != nil {
		t.Fatalf("Register bad: %v", err)
	}

	supervisor := NewSupervisor(registry, Policy{MaxPerProject: 1, MaxGlobal: 1}, 1024, time.Minute)
	defer supervisor.Close()

	if _, err := supervisor.Start(context.Background(), SessionConfig{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Start empty error=%v want %v", err, ErrInvalidArgument)
	}

	repo := t.TempDir()
	if _, err := supervisor.Start(context.Background(), SessionConfig{
		ProjectID: "project-a",
		SessionID: "session-a",
		RepoPath:  repo,
		Options:   map[string]string{"provider": "bad"},
	}); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("Start bad provider error=%v want %v", err, ErrProviderUnavailable)
	}

	if _, err := supervisor.Start(context.Background(), SessionConfig{
		ProjectID: "project-a",
		SessionID: "session-a",
		RepoPath:  repo,
		Options:   map[string]string{"provider": "fake"},
	}); err != nil {
		t.Fatalf("Start first: %v", err)
	}
	if _, err := supervisor.Start(context.Background(), SessionConfig{
		ProjectID: "project-a",
		SessionID: "session-a",
		RepoPath:  repo,
		Options:   map[string]string{"provider": "fake"},
	}); !errors.Is(err, ErrSessionAlreadyExists) {
		t.Fatalf("Start duplicate error=%v want %v", err, ErrSessionAlreadyExists)
	}
	if _, err := supervisor.Start(context.Background(), SessionConfig{
		ProjectID: "project-a",
		SessionID: "session-b",
		RepoPath:  repo,
		Options:   map[string]string{"provider": "fake"},
	}); !errors.Is(err, ErrSessionLimitReached) {
		t.Fatalf("Start limit error=%v want %v", err, ErrSessionLimitReached)
	}
}

func TestSupervisorPersistenceAndHistory(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&testProvider{id: "fake"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	dbPath := t.TempDir() + "/sessions.db"
	store, err := NewBoltSessionStore(dbPath)
	if err != nil {
		t.Fatalf("NewBoltSessionStore: %v", err)
	}

	sup := NewSupervisor(registry, DefaultPolicy(), 1024, time.Minute, WithStore(store))
	defer sup.Close()

	repo := t.TempDir()
	if _, err := sup.Start(context.Background(), SessionConfig{
		ProjectID: "proj-a",
		SessionID: "persist-1",
		RepoPath:  repo,
		Options:   map[string]string{"provider": "fake"},
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Stop the session so it reaches a terminal state and is persisted.
	if err := sup.Stop("persist-1", true); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitForStopped(t, sup, "persist-1")
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Simulate a daemon restart: open a fresh supervisor with the same store.
	store2, err := NewBoltSessionStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	sup2 := NewSupervisor(registry, DefaultPolicy(), 1024, time.Minute, WithStore(store2))
	defer sup2.Close()
	defer func() { _ = store2.Close() }()

	if err := sup2.LoadHistory(); err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}

	// The stopped session must be visible via Get and List.
	info, err := sup2.Get("persist-1")
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if info.State != SessionStateStopped && info.State != SessionStateFailed {
		t.Errorf("State=%v want Stopped or Failed", info.State)
	}
	if info.ProjectID != "proj-a" {
		t.Errorf("ProjectID=%q want %q", info.ProjectID, "proj-a")
	}

	list := sup2.List("proj-a")
	found := false
	for _, s := range list {
		if s.SessionID == "persist-1" {
			found = true
		}
	}
	if !found {
		t.Errorf("persist-1 not found in List after restart")
	}
}

func TestSupervisorHistoryOrphansMarkedFailed(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&testProvider{id: "fake"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	dbPath := t.TempDir() + "/sessions.db"

	// Seed the store with a running session (simulating a crash).
	store, err := NewBoltSessionStore(dbPath)
	if err != nil {
		t.Fatalf("NewBoltSessionStore: %v", err)
	}
	orphan := SessionInfo{
		SessionID: "orphan-1",
		ProjectID: "proj-b",
		Provider:  "fake",
		State:     SessionStateRunning,
		CreatedAt: nowUTC(),
	}
	if err := store.Save(orphan); err != nil {
		t.Fatalf("Save orphan: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Restart: orphan must be marked Failed.
	store2, err := NewBoltSessionStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	sup := NewSupervisor(registry, DefaultPolicy(), 1024, time.Minute, WithStore(store2))
	defer sup.Close()
	defer func() { _ = store2.Close() }()

	if err := sup.LoadHistory(); err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}

	info, err := sup.Get("orphan-1")
	if err != nil {
		t.Fatalf("Get orphan: %v", err)
	}
	if info.State != SessionStateFailed {
		t.Errorf("State=%v want Failed", info.State)
	}
	if info.Error == "" {
		t.Errorf("Error should be set for orphaned session")
	}
}

func TestSupervisorHistoryChunkReplay(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&testProvider{id: "fake"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	dbPath := t.TempDir() + "/sessions.db"
	store, err := NewBoltSessionStore(dbPath)
	if err != nil {
		t.Fatalf("NewBoltSessionStore: %v", err)
	}

	sup := NewSupervisor(registry, DefaultPolicy(), 1024, time.Minute, WithStore(store))
	repo := t.TempDir()
	if _, err := sup.Start(context.Background(), SessionConfig{
		ProjectID: "proj-a",
		SessionID: "replay-1",
		RepoPath:  repo,
		Options:   map[string]string{"provider": "fake"},
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Write some input so /bin/cat echoes it into the PTY buffer.
	state, err := sup.Attach("replay-1", "client-a", 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if _, err := sup.WriteInput("replay-1", "client-a", []byte("hello\n")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	waitForChunk(t, state.Live, "hello")
	if err := sup.Detach("replay-1", "client-a"); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// Stop and let the session reach a terminal state.
	if err := sup.Stop("replay-1", true); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitForStopped(t, sup, "replay-1")
	sup.Close()
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Simulate daemon restart: open a fresh supervisor with the same store.
	store2, err := NewBoltSessionStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	sup2 := NewSupervisor(registry, DefaultPolicy(), 1024, time.Minute, WithStore(store2))
	defer sup2.Close()
	defer func() { _ = store2.Close() }()

	if err := sup2.LoadHistory(); err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}

	// AttachSession on a history session must return replay chunks from the store.
	state2, err := sup2.Attach("replay-1", "client-b", 0)
	if err != nil {
		t.Fatalf("Attach history session: %v", err)
	}
	if len(state2.Replay) == 0 {
		t.Fatal("expected non-empty replay for history session")
	}
	var found bool
	for _, c := range state2.Replay {
		if bytes.Contains(c.Payload, []byte("hello")) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'hello' in history replay, got %d chunks", len(state2.Replay))
	}
	// Live channel must be closed (no running process).
	select {
	case _, ok := <-state2.Live:
		if ok {
			t.Error("live channel should be closed for history session")
		}
	default:
		t.Error("live channel should be immediately readable (closed)")
	}
}

func waitForChunk(t *testing.T, ch <-chan OutputChunk, needle string) OutputChunk {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case chunk := <-ch:
			if bytes.Contains(chunk.Payload, []byte(needle)) {
				return chunk
			}
		case <-timeout:
			t.Fatalf("timed out waiting for chunk containing %q", needle)
		}
	}
}

func waitForStopped(t *testing.T, supervisor *Supervisor, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		info, err := supervisor.Get(sessionID)
		if err == nil && info.ExitRecorded {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to stop", sessionID)
}
