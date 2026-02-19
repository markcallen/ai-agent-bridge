package server

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/internal/auth"
	"github.com/markcallen/ai-agent-bridge/internal/bridge"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type testProvider struct {
	id string
}

func (p *testProvider) ID() string { return p.id }
func (p *testProvider) Health(ctx context.Context) error {
	return nil
}
func (p *testProvider) Start(ctx context.Context, cfg bridge.SessionConfig) (bridge.SessionHandle, error) {
	h := &testHandle{id: cfg.SessionID, events: make(chan bridge.Event, 32)}
	h.events <- bridge.Event{
		Type:      bridge.EventTypeSessionStarted,
		Stream:    "system",
		Text:      "started",
		SessionID: cfg.SessionID,
		ProjectID: cfg.ProjectID,
		Provider:  p.id,
	}
	return h, nil
}
func (p *testProvider) Send(handle bridge.SessionHandle, text string) error {
	return nil
}
func (p *testProvider) Stop(handle bridge.SessionHandle) error {
	h := handle.(*testHandle)
	h.events <- bridge.Event{
		Type:   bridge.EventTypeSessionStopped,
		Stream: "system",
		Text:   "stopped",
		Done:   true,
	}
	close(h.events)
	return nil
}
func (p *testProvider) Events(handle bridge.SessionHandle) <-chan bridge.Event {
	return handle.(*testHandle).events
}

type testHandle struct {
	id     string
	events chan bridge.Event
}

func (h *testHandle) ID() string { return h.id }
func (h *testHandle) PID() int   { return 1 }

type testStream struct {
	ctx context.Context
}

func (t *testStream) SetHeader(md metadata.MD) error  { return nil }
func (t *testStream) SendHeader(md metadata.MD) error { return nil }
func (t *testStream) SetTrailer(md metadata.MD)       {}
func (t *testStream) Context() context.Context        { return t.ctx }
func (t *testStream) SendMsg(m any) error             { return nil }
func (t *testStream) RecvMsg(m any) error             { return nil }
func (t *testStream) Send(*bridgev1.SessionEvent) error {
	return nil
}

func testCtx(projectID string) context.Context {
	claims := &auth.BridgeClaims{
		ProjectID: projectID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "test-user",
		},
	}
	return auth.ContextWithClaims(context.Background(), claims)
}

func newTestServer(t *testing.T, policy bridge.Policy) *BridgeServer {
	t.Helper()
	reg := bridge.NewRegistry()
	if err := reg.Register(&testProvider{id: "test"}); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	sup := bridge.NewSupervisor(reg, policy, 64, bridge.DefaultSubscriberConfig())
	t.Cleanup(func() { sup.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(sup, reg, logger, RateLimitConfig{
		GlobalRPS:                  1000,
		GlobalBurst:                1000,
		StartSessionPerClientRPS:   1000,
		StartSessionPerClientBurst: 1000,
		SendInputPerSessionRPS:     1000,
		SendInputPerSessionBurst:   1000,
	})
}

func mustStartSession(t *testing.T, s *BridgeServer, projectID, sessionID string) {
	t.Helper()
	_, err := s.StartSession(testCtx(projectID), &bridgev1.StartSessionRequest{
		ProjectId: projectID,
		SessionId: sessionID,
		RepoPath:  t.TempDir(),
		Provider:  "test",
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
}

func TestStartSessionRejectsProjectMismatch(t *testing.T) {
	s := newTestServer(t, bridge.DefaultPolicy())
	_, err := s.StartSession(testCtx("project-a"), &bridgev1.StartSessionRequest{
		ProjectId: "project-b",
		SessionId: "00000000-0000-0000-0000-000000000001",
		RepoPath:  t.TempDir(),
		Provider:  "test",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code=%s want=%s err=%v", status.Code(err), codes.PermissionDenied, err)
	}
}

func TestSessionScopedRPCsRejectProjectMismatch(t *testing.T) {
	s := newTestServer(t, bridge.DefaultPolicy())
	mustStartSession(t, s, "project-a", "00000000-0000-0000-0000-000000000002")

	if _, err := s.GetSession(testCtx("project-b"), &bridgev1.GetSessionRequest{SessionId: "00000000-0000-0000-0000-000000000002"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("GetSession code=%s err=%v", status.Code(err), err)
	}
	if _, err := s.SendInput(testCtx("project-b"), &bridgev1.SendInputRequest{SessionId: "00000000-0000-0000-0000-000000000002", Text: "hi"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("SendInput code=%s err=%v", status.Code(err), err)
	}
	if _, err := s.StopSession(testCtx("project-b"), &bridgev1.StopSessionRequest{SessionId: "00000000-0000-0000-0000-000000000002"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("StopSession code=%s err=%v", status.Code(err), err)
	}
	if err := s.StreamEvents(&bridgev1.StreamEventsRequest{SessionId: "00000000-0000-0000-0000-000000000002"}, &testStream{ctx: testCtx("project-b")}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("StreamEvents code=%s err=%v", status.Code(err), err)
	}
}

func TestListSessionsUsesClaimProjectScope(t *testing.T) {
	s := newTestServer(t, bridge.DefaultPolicy())
	mustStartSession(t, s, "project-a", "00000000-0000-0000-0000-000000000003")
	mustStartSession(t, s, "project-b", "00000000-0000-0000-0000-000000000004")

	resp, err := s.ListSessions(testCtx("project-a"), &bridgev1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(resp.Sessions) != 1 || resp.Sessions[0].ProjectId != "project-a" {
		t.Fatalf("sessions=%v", resp.Sessions)
	}

	_, err = s.ListSessions(testCtx("project-a"), &bridgev1.ListSessionsRequest{ProjectId: "project-b"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code=%s want=%s err=%v", status.Code(err), codes.PermissionDenied, err)
	}
}

func TestErrorMapping(t *testing.T) {
	policy := bridge.DefaultPolicy()
	policy.MaxInputBytes = 1
	s := newTestServer(t, policy)

	_, err := s.StartSession(testCtx("project-a"), &bridgev1.StartSessionRequest{
		ProjectId: "project-a",
		SessionId: "00000000-0000-0000-0000-000000000005",
		RepoPath:  t.TempDir(),
		Provider:  "unknown",
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("unknown provider code=%s err=%v", status.Code(err), err)
	}

	mustStartSession(t, s, "project-a", "00000000-0000-0000-0000-000000000006")
	_, err = s.StartSession(testCtx("project-a"), &bridgev1.StartSessionRequest{
		ProjectId: "project-a",
		SessionId: "00000000-0000-0000-0000-000000000006",
		RepoPath:  t.TempDir(),
		Provider:  "test",
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate code=%s err=%v", status.Code(err), err)
	}

	_, err = s.SendInput(testCtx("project-a"), &bridgev1.SendInputRequest{
		SessionId: "00000000-0000-0000-0000-000000000006",
		Text:      "too big",
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("input too large code=%s err=%v", status.Code(err), err)
	}
}

func TestStartSessionRejectsInvalidUUID(t *testing.T) {
	s := newTestServer(t, bridge.DefaultPolicy())
	_, err := s.StartSession(testCtx("project-a"), &bridgev1.StartSessionRequest{
		ProjectId: "project-a",
		SessionId: "not-a-uuid",
		RepoPath:  t.TempDir(),
		Provider:  "test",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code=%s want=%s err=%v", status.Code(err), codes.InvalidArgument, err)
	}
}

func TestSendInputRejectsControlCharacters(t *testing.T) {
	s := newTestServer(t, bridge.DefaultPolicy())
	sessionID := "11111111-1111-1111-1111-111111111111"
	mustStartSession(t, s, "project-a", sessionID)

	_, err := s.SendInput(testCtx("project-a"), &bridgev1.SendInputRequest{
		SessionId: sessionID,
		Text:      "bad\x00input",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code=%s want=%s err=%v", status.Code(err), codes.InvalidArgument, err)
	}
}

func TestRateLimitStartSessionPerClient(t *testing.T) {
	reg := bridge.NewRegistry()
	if err := reg.Register(&testProvider{id: "test"}); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	sup := bridge.NewSupervisor(reg, bridge.DefaultPolicy(), 64, bridge.DefaultSubscriberConfig())
	t.Cleanup(func() { sup.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(sup, reg, logger, RateLimitConfig{
		GlobalRPS:                  1000,
		GlobalBurst:                1000,
		StartSessionPerClientRPS:   1,
		StartSessionPerClientBurst: 1,
		SendInputPerSessionRPS:     1000,
		SendInputPerSessionBurst:   1000,
	})
	ctx := testCtx("project-a")

	_, err := s.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId: "project-a",
		SessionId: "11111111-1111-1111-1111-111111111111",
		RepoPath:  t.TempDir(),
		Provider:  "test",
	})
	if err != nil {
		t.Fatalf("first StartSession: %v", err)
	}
	_, err = s.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId: "project-a",
		SessionId: "22222222-2222-2222-2222-222222222222",
		RepoPath:  t.TempDir(),
		Provider:  "test",
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second StartSession code=%s want=%s err=%v", status.Code(err), codes.ResourceExhausted, err)
	}
}

func TestRateLimitSendInputPerSession(t *testing.T) {
	reg := bridge.NewRegistry()
	if err := reg.Register(&testProvider{id: "test"}); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	sup := bridge.NewSupervisor(reg, bridge.DefaultPolicy(), 64, bridge.DefaultSubscriberConfig())
	t.Cleanup(func() { sup.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(sup, reg, logger, RateLimitConfig{
		GlobalRPS:                  1000,
		GlobalBurst:                1000,
		StartSessionPerClientRPS:   1000,
		StartSessionPerClientBurst: 1000,
		SendInputPerSessionRPS:     1,
		SendInputPerSessionBurst:   1,
	})
	sessionID := "33333333-3333-3333-3333-333333333333"
	mustStartSession(t, s, "project-a", sessionID)

	_, err := s.SendInput(testCtx("project-a"), &bridgev1.SendInputRequest{
		SessionId: sessionID,
		Text:      "one",
	})
	if err != nil {
		t.Fatalf("first SendInput: %v", err)
	}
	_, err = s.SendInput(testCtx("project-a"), &bridgev1.SendInputRequest{
		SessionId: sessionID,
		Text:      "two",
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second SendInput code=%s want=%s err=%v", status.Code(err), codes.ResourceExhausted, err)
	}
	time.Sleep(1100 * time.Millisecond)
	_, err = s.SendInput(testCtx("project-a"), &bridgev1.SendInputRequest{
		SessionId: sessionID,
		Text:      "three",
	})
	if err != nil {
		t.Fatalf("third SendInput after refill: %v", err)
	}
}
