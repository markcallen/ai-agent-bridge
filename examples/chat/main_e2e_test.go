package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
	"google.golang.org/grpc"
)

type testBridgeServer struct {
	bridgev1.UnimplementedBridgeServiceServer

	mu       sync.Mutex
	sessions map[string]*testSession
}

type testSession struct {
	project string
	seq     uint64
	events  []*bridgev1.SessionEvent
	subs    map[int]chan *bridgev1.SessionEvent
	nextSub int
}

func newTestBridgeServer() *testBridgeServer {
	return &testBridgeServer{
		sessions: make(map[string]*testSession),
	}
}

func (s *testBridgeServer) StartSession(_ context.Context, req *bridgev1.StartSessionRequest) (*bridgev1.StartSessionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[req.SessionId]; ok {
		return nil, errors.New("session already exists")
	}
	ss := &testSession{
		project: req.ProjectId,
		subs:    make(map[int]chan *bridgev1.SessionEvent),
	}
	s.sessions[req.SessionId] = ss
	s.appendEventLocked(req.SessionId, bridgev1.EventType_EVENT_TYPE_SESSION_STARTED, "system", "session started", "")
	return &bridgev1.StartSessionResponse{
		SessionId: req.SessionId,
		Status:    bridgev1.SessionStatus_SESSION_STATUS_RUNNING,
	}, nil
}

func (s *testBridgeServer) StopSession(_ context.Context, req *bridgev1.StopSessionRequest) (*bridgev1.StopSessionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[req.SessionId]; !ok {
		return nil, errors.New("session not found")
	}
	s.appendEventLocked(req.SessionId, bridgev1.EventType_EVENT_TYPE_SESSION_STOPPED, "system", "stopped", "")
	return &bridgev1.StopSessionResponse{Status: bridgev1.SessionStatus_SESSION_STATUS_STOPPED}, nil
}

func (s *testBridgeServer) SendInput(_ context.Context, req *bridgev1.SendInputRequest) (*bridgev1.SendInputResponse, error) {
	s.mu.Lock()
	_, ok := s.sessions[req.SessionId]
	if !ok {
		s.mu.Unlock()
		return nil, errors.New("session not found")
	}
	seq := s.appendEventLocked(req.SessionId, bridgev1.EventType_EVENT_TYPE_INPUT_RECEIVED, "system", req.Text, "")
	s.mu.Unlock()

	// Deterministic fake behavior:
	// - "no-output" prompts never emit stdout.
	// - "instant-output" prompts emit immediately.
	// - all other prompts emit delayed stdout.
	if !strings.Contains(req.Text, "no-output") {
		delay := 120 * time.Millisecond
		if strings.Contains(req.Text, "instant-output") {
			delay = 0
		}
		go func(text string) {
			if delay > 0 {
				time.Sleep(delay)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			_ = s.appendEventLocked(req.SessionId, bridgev1.EventType_EVENT_TYPE_STDOUT, "stdout", "echo:"+text, "")
		}(req.Text)
	}

	return &bridgev1.SendInputResponse{
		Accepted: true,
		Seq:      seq,
	}, nil
}

func (s *testBridgeServer) StreamEvents(req *bridgev1.StreamEventsRequest, stream bridgev1.BridgeService_StreamEventsServer) error {
	s.mu.Lock()
	ss, ok := s.sessions[req.SessionId]
	if !ok {
		s.mu.Unlock()
		return errors.New("session not found")
	}

	history := make([]*bridgev1.SessionEvent, 0, len(ss.events))
	for _, ev := range ss.events {
		if ev.Seq > req.AfterSeq {
			history = append(history, ev)
		}
	}

	subID := ss.nextSub
	ss.nextSub++
	ch := make(chan *bridgev1.SessionEvent, 64)
	ss.subs[subID] = ch
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(ss.subs, subID)
		close(ch)
		s.mu.Unlock()
	}()

	for _, ev := range history {
		if err := stream.Send(ev); err != nil {
			return err
		}
	}

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case ev := <-ch:
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
	}
}

func (s *testBridgeServer) appendEventLocked(sessionID string, typ bridgev1.EventType, stream, text, errText string) uint64 {
	ss := s.sessions[sessionID]
	ss.seq++
	ev := &bridgev1.SessionEvent{
		Seq:       ss.seq,
		SessionId: sessionID,
		ProjectId: ss.project,
		Type:      typ,
		Stream:    stream,
		Text:      text,
		Error:     errText,
	}
	ss.events = append(ss.events, ev)
	for _, sub := range ss.subs {
		select {
		case sub <- ev:
		default:
		}
	}
	return ev.Seq
}

func startTestClient(t *testing.T) (*bridgeclient.Client, *testBridgeServer, func()) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	fake := newTestBridgeServer()
	bridgev1.RegisterBridgeServiceServer(grpcServer, fake)

	go func() {
		_ = grpcServer.Serve(lis)
	}()

	client, err := bridgeclient.New(
		bridgeclient.WithTarget(lis.Addr().String()),
		bridgeclient.WithTimeout(2*time.Second),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	cleanup := func() {
		_ = client.Close()
		grpcServer.Stop()
		_ = lis.Close()
	}
	return client, fake, cleanup
}

func startTrackerStream(t *testing.T, client *bridgeclient.Client, sessionID string, tracker *responseTracker, staleSeqGate chan struct{}, staleSeq uint64) (context.CancelFunc, chan error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := client.StreamEvents(ctx, &bridgev1.StreamEventsRequest{
		SessionId: sessionID,
		AfterSeq:  0,
	})
	if err != nil {
		t.Fatalf("stream events: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- stream.RecvAll(ctx, func(ev *bridgev1.SessionEvent) error {
			switch ev.Type {
			case bridgev1.EventType_EVENT_TYPE_STDOUT, bridgev1.EventType_EVENT_TYPE_STDERR:
				if staleSeqGate != nil && ev.Seq == staleSeq {
					<-staleSeqGate
				}
				tracker.onOutput(ev, 40*time.Millisecond)
			case bridgev1.EventType_EVENT_TYPE_RESPONSE_COMPLETE:
				tracker.onResponseComplete()
			case bridgev1.EventType_EVENT_TYPE_SESSION_FAILED, bridgev1.EventType_EVENT_TYPE_SESSION_STOPPED:
				tracker.onTerminal(ev)
			}
			return nil
		})
	}()

	return cancel, done
}

func TestSendPromptWaitsForFreshOutput(t *testing.T) {
	client, _, cleanup := startTestClient(t)
	defer cleanup()

	sessionID := "session-waits"
	_, err := client.StartSession(context.Background(), &bridgev1.StartSessionRequest{
		ProjectId: "test",
		SessionId: sessionID,
		RepoPath:  ".",
		Provider:  "echo",
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	tracker := newResponseTracker()
	cancel, recvDone := startTrackerStream(t, client, sessionID, tracker, nil, 0)
	defer cancel()

	start := time.Now()
	err = sendPrompt(client, sessionID, "hello", 1*time.Second, tracker, 40*time.Millisecond)
	if err != nil {
		t.Fatalf("sendPrompt: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Fatalf("sendPrompt returned too quickly: %s", elapsed)
	}

	cancel()
	<-recvDone
}

func TestSendPromptIgnoresStaleOutputFromPriorInput(t *testing.T) {
	client, _, cleanup := startTestClient(t)
	defer cleanup()

	sessionID := "session-stale"
	_, err := client.StartSession(context.Background(), &bridgev1.StartSessionRequest{
		ProjectId: "test",
		SessionId: sessionID,
		RepoPath:  ".",
		Provider:  "echo",
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	tracker := newResponseTracker()
	gate := make(chan struct{})
	// First input emits stdout immediately. We'll hold that stdout event in the
	// stream callback so it arrives late and appears stale to the second prompt.
	firstResp, err := client.SendInput(context.Background(), &bridgev1.SendInputRequest{
		SessionId: sessionID,
		Text:      "instant-output:first",
	})
	if err != nil {
		t.Fatalf("first send input: %v", err)
	}
	staleSeq := firstResp.Seq + 1

	cancel, recvDone := startTrackerStream(t, client, sessionID, tracker, gate, staleSeq)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- sendPrompt(client, sessionID, "no-output", 250*time.Millisecond, tracker, 40*time.Millisecond)
	}()

	// Release stale stdout delivery after second prompt waiting has started.
	time.Sleep(30 * time.Millisecond)
	close(gate)

	err = <-done
	if err == nil {
		t.Fatal("expected timeout error; got nil")
	}
	if !strings.Contains(err.Error(), "timed out waiting for response") {
		t.Fatalf("unexpected error: %v", err)
	}

	cancel()
	<-recvDone
}
