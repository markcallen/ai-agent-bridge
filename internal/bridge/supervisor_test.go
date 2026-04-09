package bridge

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"syscall"
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

func TestSupervisorLoadHistoryRecoversRunningProcess(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&testProvider{id: "fake"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	cmd := exec.Command("/bin/sh", "-c", "sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
		}
	})

	dbPath := t.TempDir() + "/sessions.db"
	store, err := NewBoltSessionStore(dbPath)
	if err != nil {
		t.Fatalf("NewBoltSessionStore: %v", err)
	}
	recovered := SessionInfo{
		SessionID: "recover-1",
		ProjectID: "proj-r",
		Provider:  "fake",
		State:     SessionStateRunning,
		CreatedAt: nowUTC(),
		ProcessID: cmd.Process.Pid,
	}
	if err := store.Save(recovered); err != nil {
		t.Fatalf("Save recovered session: %v", err)
	}
	chunk := OutputChunk{Seq: 1, Timestamp: nowUTC(), Payload: []byte("persisted output")}
	if err := store.SaveChunk("recover-1", chunk); err != nil {
		t.Fatalf("SaveChunk: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

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

	info, err := sup.Get("recover-1")
	if err != nil {
		t.Fatalf("Get recover-1: %v", err)
	}
	if info.State != SessionStateRunning {
		t.Fatalf("State=%v want Running", info.State)
	}
	if !info.Recovered {
		t.Fatal("Recovered flag was false")
	}

	attach, err := sup.Attach("recover-1", "client-a", 0)
	if err != nil {
		t.Fatalf("Attach recovered: %v", err)
	}
	if len(attach.Replay) != 1 {
		t.Fatalf("Replay len=%d want 1", len(attach.Replay))
	}
	select {
	case _, ok := <-attach.Live:
		if ok {
			t.Fatal("recovered live channel should be closed")
		}
	default:
		t.Fatal("recovered live channel should be immediately closed")
	}

	if _, err := sup.WriteInput("recover-1", "client-a", []byte("hello")); !errors.Is(err, ErrSessionRecoveryUnavailable) {
		t.Fatalf("WriteInput recovered error=%v want %v", err, ErrSessionRecoveryUnavailable)
	}

	if err := sup.Stop("recover-1", true); err != nil {
		t.Fatalf("Stop recovered: %v", err)
	}
	waitForRecoveredStopped(t, sup, "recover-1")
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

// streamJSONTestProvider wraps testProvider and implements StreamJSONProvider.
// BuildCommand runs a shell one-liner that prints a fixed JSONL payload and exits.
type streamJSONTestProvider struct {
	testProvider
	jsonLines []string
}

func (p *streamJSONTestProvider) IsStreamJSON() bool { return true }

func (p *streamJSONTestProvider) BuildCommand(ctx context.Context, cfg SessionConfig) (*exec.Cmd, error) {
	// Construct a printf call that emits each line.
	args := make([]string, 0, len(p.jsonLines)*2+2)
	args = append(args, "-c")
	script := ""
	for _, line := range p.jsonLines {
		script += "printf '%s\\n' '" + line + "';"
	}
	args = append(args, script)
	cmd := exec.CommandContext(ctx, "/bin/sh", args...)
	cmd.Dir = cfg.RepoPath
	return cmd, nil
}

func TestReadLoopStreamJSONParsing(t *testing.T) {
	sup := NewSupervisor(NewRegistry(), DefaultPolicy(), 64*1024, time.Minute)
	defer sup.Close()

	ms := &managedSession{
		buf:  NewByteBuffer(64 * 1024),
		live: make(chan OutputChunk, 100),
		info: SessionInfo{SessionID: "test-stream"},
	}

	lines := []string{
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello world"}}`,
		`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"deep thought"}}`,
		`not json at all`,
		`{"type":"other_event"}`,
	}
	pr, pw := io.Pipe()
	go func() {
		for _, line := range lines {
			_, _ = pw.Write([]byte(line + "\n"))
		}
		_ = pw.Close()
	}()

	// readLoopStreamJSON blocks until EOF; closeLive closes ms.live on return.
	sup.readLoopStreamJSON(ms, pr)

	chunks := ms.buf.After(0)
	if len(chunks) == 0 {
		t.Fatal("expected chunks in buffer, got none")
	}

	var textChunks, thinkingChunks, rawChunks []OutputChunk
	for _, c := range chunks {
		switch c.Type {
		case ChunkTypeOutput:
			textChunks = append(textChunks, c)
		case ChunkTypeThinking:
			thinkingChunks = append(thinkingChunks, c)
		}
		rawChunks = append(rawChunks, c)
	}

	// text_delta → ChunkTypeOutput
	if len(textChunks) == 0 {
		t.Error("expected at least one ChunkTypeOutput chunk")
	}
	var foundText bool
	for _, c := range textChunks {
		if bytes.Contains(c.Payload, []byte("hello world")) {
			foundText = true
		}
	}
	if !foundText {
		t.Errorf("expected 'hello world' in ChunkTypeOutput chunks, got %v", textChunks)
	}

	// thinking_delta → ChunkTypeThinking
	if len(thinkingChunks) == 0 {
		t.Error("expected at least one ChunkTypeThinking chunk")
	}
	var foundThinking bool
	for _, c := range thinkingChunks {
		if bytes.Contains(c.Payload, []byte("deep thought")) {
			foundThinking = true
		}
	}
	if !foundThinking {
		t.Errorf("expected 'deep thought' in ChunkTypeThinking chunks, got %v", thinkingChunks)
	}

	// Non-JSON line → raw ChunkTypeOutput chunk
	var foundRaw bool
	for _, c := range rawChunks {
		if bytes.Contains(c.Payload, []byte("not json")) {
			foundRaw = true
		}
	}
	if !foundRaw {
		t.Error("expected non-JSON line to be emitted as raw ChunkTypeOutput chunk")
	}
}

func TestStreamJSONSessionLifecycle(t *testing.T) {
	jsonLines := []string{
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"answer"}}`,
		`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"thinking"}}`,
	}
	p := &streamJSONTestProvider{
		testProvider: testProvider{id: "stream-fake"},
		jsonLines:    jsonLines,
	}
	registry := NewRegistry()
	if err := registry.Register(p); err != nil {
		t.Fatalf("Register: %v", err)
	}

	sup := NewSupervisor(registry, DefaultPolicy(), 64*1024, time.Minute)
	defer sup.Close()

	repo := t.TempDir()
	info, err := sup.Start(context.Background(), SessionConfig{
		ProjectID: "proj-stream",
		SessionID: "stream-1",
		RepoPath:  repo,
		Options:   map[string]string{"provider": "stream-fake"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if info.Provider != "stream-fake" {
		t.Fatalf("Provider=%q want stream-fake", info.Provider)
	}

	// Attach and wait for at least one chunk from the process output.
	state, err := sup.Attach("stream-1", "client-x", 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	// Drain until the live channel closes (process exits).
	var collected []OutputChunk
	timeout := time.After(5 * time.Second)
drainLoop:
	for {
		select {
		case c, ok := <-state.Live:
			if !ok {
				break drainLoop
			}
			collected = append(collected, c)
		case <-timeout:
			t.Fatal("timed out waiting for stream-JSON session to complete")
		}
	}

	// Check for text and thinking chunks.
	var sawText, sawThinking bool
	for _, c := range collected {
		if c.Type == ChunkTypeOutput && bytes.Contains(c.Payload, []byte("answer")) {
			sawText = true
		}
		if c.Type == ChunkTypeThinking && bytes.Contains(c.Payload, []byte("thinking")) {
			sawThinking = true
		}
	}
	if !sawText {
		t.Errorf("expected text chunk with 'answer', got %d chunks", len(collected))
	}
	if !sawThinking {
		t.Errorf("expected thinking chunk with 'thinking', got %d chunks", len(collected))
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

func TestSupervisorFallbackProvider(t *testing.T) {
	registry := NewRegistry()
	_ = registry.Register(&testProvider{id: "primary", healthErr: errors.New("down")})
	_ = registry.Register(&testProvider{id: "fallback1", healthErr: errors.New("also down")})
	_ = registry.Register(&testProvider{id: "fallback2"})

	supervisor := NewSupervisor(registry, DefaultPolicy(), 1024, time.Minute)
	defer supervisor.Close()

	repo := t.TempDir()

	t.Run("primary succeeds, no fallback used", func(t *testing.T) {
		_ = registry.Register(&testProvider{id: "ok"})
		info, err := supervisor.Start(context.Background(), SessionConfig{
			ProjectID: "project-a",
			SessionID: "s-ok",
			RepoPath:  repo,
			Options:   map[string]string{"provider": "ok"},
			Fallbacks: []string{"fallback2"},
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if info.Provider != "ok" {
			t.Fatalf("Provider=%q want ok", info.Provider)
		}
		_ = supervisor.Stop("s-ok", true)
		waitForStopped(t, supervisor, "s-ok")
	})

	t.Run("primary down, first fallback down, second succeeds", func(t *testing.T) {
		info, err := supervisor.Start(context.Background(), SessionConfig{
			ProjectID: "project-a",
			SessionID: "s-fallback",
			RepoPath:  repo,
			Options:   map[string]string{"provider": "primary"},
			Fallbacks: []string{"fallback1", "fallback2"},
		})
		if err != nil {
			t.Fatalf("Start with fallback: %v", err)
		}
		if info.Provider != "fallback2" {
			t.Fatalf("Provider=%q want fallback2", info.Provider)
		}
		_ = supervisor.Stop("s-fallback", true)
		waitForStopped(t, supervisor, "s-fallback")
	})

	t.Run("all providers down returns error", func(t *testing.T) {
		_, err := supervisor.Start(context.Background(), SessionConfig{
			ProjectID: "project-a",
			SessionID: "s-allfail",
			RepoPath:  repo,
			Options:   map[string]string{"provider": "primary"},
			Fallbacks: []string{"fallback1"},
		})
		if !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("Start all-down error=%v want %v", err, ErrProviderUnavailable)
		}
	})

	t.Run("unknown primary with no fallbacks returns error", func(t *testing.T) {
		_, err := supervisor.Start(context.Background(), SessionConfig{
			ProjectID: "project-a",
			SessionID: "s-unknown",
			RepoPath:  repo,
			Options:   map[string]string{"provider": "nonexistent"},
		})
		if !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("Start unknown error=%v want %v", err, ErrProviderUnavailable)
		}
	})
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

func waitForRecoveredStopped(t *testing.T, supervisor *Supervisor, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		info, err := supervisor.Get(sessionID)
		if err == nil && (info.State == SessionStateStopped || info.State == SessionStateFailed) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for recovered session %q to stop", sessionID)
}
