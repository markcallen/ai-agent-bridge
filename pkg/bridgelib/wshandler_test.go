package bridgelib

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// testWSHandler creates a WSHandler backed by a minimal Bridge wired to a
// fake provider that exits immediately.
func testWSHandler(t *testing.T) *WSHandler {
	t.Helper()
	b, err := New(Config{
		Providers: []ProviderConfig{
			{
				ID:             "claude",
				Binary:         "false",
				Args:           []string{},
				StartupTimeout: 50 * time.Millisecond,
				StopGrace:      50 * time.Millisecond,
			},
		},
		EventBufferSize: 64,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(b.Close)
	return NewWSHandler(b)
}

// collect gathers all JSON messages sent via sendFn into a slice.
type collector struct {
	mu   sync.Mutex
	msgs []map[string]any
}

func (c *collector) fn() func([]byte) {
	return func(b []byte) {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return
		}
		c.mu.Lock()
		c.msgs = append(c.msgs, m)
		c.mu.Unlock()
	}
}

func (c *collector) first() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.msgs) == 0 {
		return nil
	}
	return c.msgs[0]
}

func (c *collector) waitForType(t *testing.T, msgType string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		for _, m := range c.msgs {
			if m["type"] == msgType {
				c.mu.Unlock()
				return m
			}
		}
		c.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for message type %q", msgType)
	return nil
}

// TestHandleMessage_UnknownType verifies that an unrecognised type is silently ignored.
func TestHandleMessage_UnknownType(t *testing.T) {
	h := testWSHandler(t)
	col := &collector{}
	ctx := context.Background()
	h.HandleMessage(ctx, "conn1", []byte(`{"type":"file_update","path":"/foo"}`), col.fn())
	time.Sleep(20 * time.Millisecond)
	if len(col.msgs) != 0 {
		t.Fatalf("expected no response for unknown type, got %v", col.msgs)
	}
}

// TestHandleMessage_BadJSON verifies that invalid JSON is silently ignored.
func TestHandleMessage_BadJSON(t *testing.T) {
	h := testWSHandler(t)
	col := &collector{}
	h.HandleMessage(context.Background(), "conn1", []byte(`{not json`), col.fn())
	time.Sleep(20 * time.Millisecond)
	if len(col.msgs) != 0 {
		t.Fatalf("expected no response for bad JSON, got %v", col.msgs)
	}
}

// TestHandleMessage_EmptyType verifies a message with no type field is ignored.
func TestHandleMessage_EmptyType(t *testing.T) {
	h := testWSHandler(t)
	col := &collector{}
	h.HandleMessage(context.Background(), "conn1", []byte(`{"foo":"bar"}`), col.fn())
	time.Sleep(20 * time.Millisecond)
	if len(col.msgs) != 0 {
		t.Fatalf("expected no response for empty type, got %v", col.msgs)
	}
}

// TestHandleMessage_ListSessions verifies list_sessions returns sessions_list.
func TestHandleMessage_ListSessions(t *testing.T) {
	h := testWSHandler(t)
	col := &collector{}
	h.HandleMessage(context.Background(), "conn1", []byte(`{"type":"list_sessions"}`), col.fn())
	time.Sleep(50 * time.Millisecond)
	msg := col.first()
	if msg == nil {
		t.Fatal("expected response, got none")
	}
	if msg["type"] != "sessions_list" {
		t.Fatalf("got type=%q, want sessions_list", msg["type"])
	}
}

// TestHandleMessage_ListSessionsByProject filters by project.
func TestHandleMessage_ListSessionsByProject(t *testing.T) {
	h := testWSHandler(t)
	col := &collector{}
	h.HandleMessage(context.Background(), "conn1",
		[]byte(`{"type":"list_sessions","projectId":"my-project"}`), col.fn())
	time.Sleep(50 * time.Millisecond)
	msg := col.first()
	if msg == nil || msg["type"] != "sessions_list" {
		t.Fatalf("expected sessions_list, got %v", msg)
	}
}

// TestHandleMessage_GetSession_NotFound verifies get_session returns an error
// for an unknown session.
func TestHandleMessage_GetSession_NotFound(t *testing.T) {
	h := testWSHandler(t)
	col := &collector{}
	h.HandleMessage(context.Background(), "conn1",
		[]byte(`{"type":"get_session","sessionId":"unknown"}`), col.fn())
	time.Sleep(50 * time.Millisecond)
	msg := col.first()
	if msg == nil {
		t.Fatal("expected response, got none")
	}
	if msg["type"] != "error" {
		t.Fatalf("got type=%q, want error", msg["type"])
	}
}

// TestHandleMessage_SendInput_NotFound verifies send_input returns an error
// for an unknown session.
func TestHandleMessage_SendInput_NotFound(t *testing.T) {
	h := testWSHandler(t)
	col := &collector{}
	h.HandleMessage(context.Background(), "conn1",
		[]byte(`{"type":"send_input","sessionId":"unknown","text":"hello"}`), col.fn())
	time.Sleep(50 * time.Millisecond)
	msg := col.first()
	if msg == nil || msg["type"] != "error" {
		t.Fatalf("expected error, got %v", msg)
	}
	if !strings.Contains(msg["code"].(string), "send_failed") {
		t.Fatalf("expected code=send_failed, got %v", msg["code"])
	}
}

// TestHandleMessage_StopSession_NotFound verifies stop_session returns an error
// for an unknown session.
func TestHandleMessage_StopSession_NotFound(t *testing.T) {
	h := testWSHandler(t)
	col := &collector{}
	h.HandleMessage(context.Background(), "conn1",
		[]byte(`{"type":"stop_session","sessionId":"unknown"}`), col.fn())
	time.Sleep(50 * time.Millisecond)
	msg := col.first()
	if msg == nil || msg["type"] != "error" {
		t.Fatalf("expected error, got %v", msg)
	}
}

// TestHandleMessage_StreamEvents_NotFound verifies stream_events returns an
// error for an unknown session.
func TestHandleMessage_StreamEvents_NotFound(t *testing.T) {
	h := testWSHandler(t)
	col := &collector{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	h.HandleMessage(ctx, "conn1",
		[]byte(`{"type":"stream_events","sessionId":"unknown","subscriberId":"sub1"}`), col.fn())
	col.waitForType(t, "error")
}

// TestHandleMessage_Health verifies health returns a health_response.
func TestHandleMessage_Health(t *testing.T) {
	h := testWSHandler(t)
	col := &collector{}
	h.HandleMessage(context.Background(), "conn1", []byte(`{"type":"health"}`), col.fn())
	time.Sleep(50 * time.Millisecond)
	msg := col.first()
	if msg == nil {
		t.Fatal("expected response, got none")
	}
	if msg["type"] != "health_response" {
		t.Fatalf("got type=%q, want health_response", msg["type"])
	}
	if _, ok := msg["healthy"]; !ok {
		t.Fatal("health_response missing 'healthy' field")
	}
}

// TestHandleMessage_ListProviders verifies list_providers returns providers_list.
func TestHandleMessage_ListProviders(t *testing.T) {
	h := testWSHandler(t)
	col := &collector{}
	h.HandleMessage(context.Background(), "conn1", []byte(`{"type":"list_providers"}`), col.fn())
	time.Sleep(50 * time.Millisecond)
	msg := col.first()
	if msg == nil || msg["type"] != "providers_list" {
		t.Fatalf("expected providers_list, got %v", msg)
	}
}

// TestConnClosed_NoPanic verifies ConnClosed is safe to call for an unknown
// connection and for a connection with active streams.
func TestConnClosed_NoPanic(t *testing.T) {
	h := testWSHandler(t)
	h.ConnClosed("nonexistent-conn") // must not panic

	// Register a fake stream entry and verify it is cancelled on close.
	cancelled := false
	h.mu.Lock()
	h.streams["conn2"] = map[string]context.CancelFunc{
		"sub1": func() { cancelled = true },
	}
	h.mu.Unlock()
	h.ConnClosed("conn2")
	if !cancelled {
		t.Fatal("expected stream cancel to be called on ConnClosed")
	}
	h.mu.Lock()
	_, exists := h.streams["conn2"]
	h.mu.Unlock()
	if exists {
		t.Fatal("expected conn2 to be removed from streams map")
	}
}

// TestHandleMessage_StartSession_ExecFails verifies start_session returns an
// error when the provider binary fails to start.
func TestHandleMessage_StartSession_ExecFails(t *testing.T) {
	h := testWSHandler(t)
	col := &collector{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	h.HandleMessage(ctx, "conn1", []byte(`{
		"type":      "start_session",
		"projectId": "proj1",
		"sessionId": "ses1",
		"repoPath":  "/tmp",
		"provider":  "claude"
	}`), col.fn())
	// The "false" binary exits immediately with status 1; the supervisor should
	// detect this and the session should fail.  We may get either an error or a
	// session_started (if it starts before the process dies).  Either is fine;
	// we just check we get *some* response without hanging.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		col.mu.Lock()
		n := len(col.msgs)
		col.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for any response from start_session")
}

// --- helper unit tests ---

func TestStateToString(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "unspecified"},
		{1, "starting"},
		{2, "running"},
		{3, "stopping"},
		{4, "stopped"},
		{5, "failed"},
		{99, "unspecified"},
	}
	for _, tc := range cases {
		if got := stateToString(tc.in); got != tc.want {
			t.Errorf("stateToString(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSessionInfoToMap(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	stopped := now.Add(time.Minute)
	s := &SessionInfo{
		SessionID: "sid",
		ProjectID: "pid",
		Provider:  "claude",
		State:     2,
		CreatedAt: now,
		StoppedAt: stopped,
		Error:     "some error",
	}
	m := sessionInfoToMap(s)
	if m["sessionId"] != "sid" {
		t.Errorf("sessionId = %v", m["sessionId"])
	}
	if m["status"] != "running" {
		t.Errorf("status = %v", m["status"])
	}
	if _, ok := m["stoppedAt"]; !ok {
		t.Error("missing stoppedAt")
	}
	if m["error"] != "some error" {
		t.Errorf("error = %v", m["error"])
	}
}

func TestSessionInfoToMap_NoStoppedAt(t *testing.T) {
	s := &SessionInfo{
		SessionID: "sid",
		ProjectID: "pid",
		State:     1,
		CreatedAt: time.Now(),
	}
	m := sessionInfoToMap(s)
	if _, ok := m["stoppedAt"]; ok {
		t.Error("unexpected stoppedAt in map")
	}
	if _, ok := m["error"]; ok {
		t.Error("unexpected error in map")
	}
}

func TestHandleMessage_InvalidJSON_StartSession(t *testing.T) {
	h := testWSHandler(t)
	col := &collector{}
	// Valid outer JSON but inner fields are wrong types
	h.HandleMessage(context.Background(), "conn1",
		[]byte(`{"type":"send_input","sessionId":123}`), col.fn())
	time.Sleep(50 * time.Millisecond)
	msg := col.first()
	if msg == nil || msg["type"] != "error" {
		t.Fatalf("expected error for bad field type, got %v", msg)
	}
}
