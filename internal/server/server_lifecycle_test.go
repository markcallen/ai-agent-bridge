package server

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/internal/auth"
	"github.com/markcallen/ai-agent-bridge/internal/bridge"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type attachStream struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	events []*bridgev1.AttachSessionEvent
}

func newAttachStream(ctx context.Context) *attachStream {
	streamCtx, cancel := context.WithCancel(ctx)
	return &attachStream{ctx: streamCtx, cancel: cancel}
}

func (s *attachStream) SetHeader(metadata.MD) error  { return nil }
func (s *attachStream) SendHeader(metadata.MD) error { return nil }
func (s *attachStream) SetTrailer(metadata.MD)       {}
func (s *attachStream) Context() context.Context     { return s.ctx }
func (s *attachStream) SendMsg(any) error            { return nil }
func (s *attachStream) RecvMsg(any) error            { return nil }
func (s *attachStream) Send(ev *bridgev1.AttachSessionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	return nil
}

func (s *attachStream) snapshot() []*bridgev1.AttachSessionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*bridgev1.AttachSessionEvent, len(s.events))
	copy(out, s.events)
	return out
}

func TestBridgeServerSessionLifecycle(t *testing.T) {
	registry := bridge.NewRegistry()
	if err := registry.Register(&serverTestProvider{id: "cat", version: "1"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	supervisor := bridge.NewSupervisor(registry, bridge.DefaultPolicy(), 1024, time.Minute)
	defer supervisor.Close()

	s := New(supervisor, registry, nil, RateLimitConfig{
		GlobalRPS:                  10,
		GlobalBurst:                10,
		StartSessionPerClientRPS:   10,
		StartSessionPerClientBurst: 10,
		SendInputPerSessionRPS:     10,
		SendInputPerSessionBurst:   10,
	})

	sessionID := uuid.NewString()
	ctx := auth.ContextWithClaims(context.Background(), &auth.BridgeClaims{ProjectID: "project-a"})

	startResp, err := s.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   "project-a",
		SessionId:   sessionID,
		RepoPath:    t.TempDir(),
		Provider:    "cat",
		InitialCols: 80,
		InitialRows: 24,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if startResp.GetSessionId() != sessionID {
		t.Fatalf("StartSession resp=%+v", startResp)
	}

	getResp, err := s.GetSession(ctx, &bridgev1.GetSessionRequest{SessionId: sessionID})
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if getResp.GetSessionId() != sessionID {
		t.Fatalf("GetSession resp=%+v", getResp)
	}

	listResp, err := s.ListSessions(ctx, &bridgev1.ListSessionsRequest{ProjectId: "project-a"})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(listResp.GetSessions()) != 1 {
		t.Fatalf("ListSessions len=%d want 1", len(listResp.GetSessions()))
	}

	stream := newAttachStream(ctx)
	attachDone := make(chan error, 1)
	go func() {
		attachDone <- s.AttachSession(&bridgev1.AttachSessionRequest{
			SessionId: sessionID,
			ClientId:  "client-a",
		}, stream)
	}()

	waitForAttachEvent(t, stream, bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ATTACHED)

	writeResp, err := s.WriteInput(ctx, &bridgev1.WriteInputRequest{
		SessionId: sessionID,
		ClientId:  "client-a",
		Data:      []byte("hello\n"),
	})
	if err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	if !writeResp.GetAccepted() {
		t.Fatalf("WriteInput resp=%+v", writeResp)
	}

	if err := waitForAttachOutput(stream, "hello"); err != nil {
		t.Fatal(err)
	}

	resizeResp, err := s.ResizeSession(ctx, &bridgev1.ResizeSessionRequest{
		SessionId: sessionID,
		ClientId:  "client-a",
		Cols:      100,
		Rows:      40,
	})
	if err != nil {
		t.Fatalf("ResizeSession: %v", err)
	}
	if !resizeResp.GetApplied() {
		t.Fatalf("ResizeSession resp=%+v", resizeResp)
	}

	stream.cancel()
	if err := <-attachDone; err != nil {
		t.Fatalf("AttachSession: %v", err)
	}

	stopResp, err := s.StopSession(ctx, &bridgev1.StopSessionRequest{SessionId: sessionID, Force: true})
	if err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	if stopResp.GetStatus() != bridgev1.SessionStatus_SESSION_STATUS_STOPPING {
		t.Fatalf("StopSession resp=%+v", stopResp)
	}
}

func TestBridgeServerValidationAndPermissions(t *testing.T) {
	registry := bridge.NewRegistry()
	supervisor := bridge.NewSupervisor(registry, bridge.DefaultPolicy(), 1024, time.Minute)
	defer supervisor.Close()

	s := New(supervisor, registry, nil, RateLimitConfig{
		GlobalRPS:   10,
		GlobalBurst: 10,
	})
	ctx := auth.ContextWithClaims(context.Background(), &auth.BridgeClaims{ProjectID: "project-a"})

	if _, err := s.ListSessions(ctx, &bridgev1.ListSessionsRequest{ProjectId: "project-b"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ListSessions code=%v want %v", status.Code(err), codes.PermissionDenied)
	}
	if _, err := s.StartSession(ctx, &bridgev1.StartSessionRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("StartSession code=%v want %v", status.Code(err), codes.InvalidArgument)
	}
	if _, err := s.GetSession(ctx, &bridgev1.GetSessionRequest{SessionId: uuid.NewString()}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetSession code=%v want %v", status.Code(err), codes.NotFound)
	}
	if _, err := s.Health(context.Background(), &bridgev1.HealthRequest{}); err != nil {
		t.Fatalf("Health: %v", err)
	}
}

func waitForAttachEvent(t *testing.T, stream *attachStream, typ bridgev1.AttachEventType) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range stream.snapshot() {
			if ev.GetType() == typ {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for attach event %v", typ)
}

func waitForAttachOutput(stream *attachStream, needle string) error {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range stream.snapshot() {
			if ev.GetType() == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_OUTPUT && bytes.Contains(ev.GetPayload(), []byte(needle)) {
				return nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return status.Error(codes.DeadlineExceeded, "timed out waiting for attach output")
}
