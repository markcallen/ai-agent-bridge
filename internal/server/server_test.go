package server

import (
	"context"
	"io"
	"log/slog"
	"testing"

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
	return New(sup, reg, logger)
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
		SessionId: "s-1",
		RepoPath:  t.TempDir(),
		Provider:  "test",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code=%s want=%s err=%v", status.Code(err), codes.PermissionDenied, err)
	}
}

func TestSessionScopedRPCsRejectProjectMismatch(t *testing.T) {
	s := newTestServer(t, bridge.DefaultPolicy())
	mustStartSession(t, s, "project-a", "s-1")

	if _, err := s.GetSession(testCtx("project-b"), &bridgev1.GetSessionRequest{SessionId: "s-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("GetSession code=%s err=%v", status.Code(err), err)
	}
	if _, err := s.SendInput(testCtx("project-b"), &bridgev1.SendInputRequest{SessionId: "s-1", Text: "hi"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("SendInput code=%s err=%v", status.Code(err), err)
	}
	if _, err := s.StopSession(testCtx("project-b"), &bridgev1.StopSessionRequest{SessionId: "s-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("StopSession code=%s err=%v", status.Code(err), err)
	}
	if err := s.StreamEvents(&bridgev1.StreamEventsRequest{SessionId: "s-1"}, &testStream{ctx: testCtx("project-b")}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("StreamEvents code=%s err=%v", status.Code(err), err)
	}
}

func TestListSessionsUsesClaimProjectScope(t *testing.T) {
	s := newTestServer(t, bridge.DefaultPolicy())
	mustStartSession(t, s, "project-a", "s-a")
	mustStartSession(t, s, "project-b", "s-b")

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
		SessionId: "missing-provider",
		RepoPath:  t.TempDir(),
		Provider:  "unknown",
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("unknown provider code=%s err=%v", status.Code(err), err)
	}

	mustStartSession(t, s, "project-a", "dup")
	_, err = s.StartSession(testCtx("project-a"), &bridgev1.StartSessionRequest{
		ProjectId: "project-a",
		SessionId: "dup",
		RepoPath:  t.TempDir(),
		Provider:  "test",
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate code=%s err=%v", status.Code(err), err)
	}

	_, err = s.SendInput(testCtx("project-a"), &bridgev1.SendInputRequest{
		SessionId: "dup",
		Text:      "too big",
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("input too large code=%s err=%v", status.Code(err), err)
	}
}
