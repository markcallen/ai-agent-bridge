package server

import (
	"context"
	"log/slog"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/internal/auth"
	"github.com/markcallen/ai-agent-bridge/internal/bridge"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// BridgeServer implements the bridge.v1.BridgeService gRPC service.
type BridgeServer struct {
	bridgev1.UnimplementedBridgeServiceServer

	supervisor *bridge.Supervisor
	registry   *bridge.Registry
	logger     *slog.Logger
}

// New creates a new BridgeServer.
func New(supervisor *bridge.Supervisor, registry *bridge.Registry, logger *slog.Logger) *BridgeServer {
	return &BridgeServer{
		supervisor: supervisor,
		registry:   registry,
		logger:     logger,
	}
}

func (s *BridgeServer) StartSession(ctx context.Context, req *bridgev1.StartSessionRequest) (*bridgev1.StartSessionResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing claims")
	}

	if req.ProjectId == "" || req.SessionId == "" || req.RepoPath == "" || req.Provider == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id, session_id, repo_path, and provider are required")
	}

	// Authorization: JWT project_id must match request
	if claims.ProjectID != "" && claims.ProjectID != req.ProjectId {
		return nil, status.Errorf(codes.PermissionDenied, "token project_id %q does not match request %q", claims.ProjectID, req.ProjectId)
	}

	opts := map[string]string{"provider": req.Provider}
	for k, v := range req.AgentOpts {
		opts[k] = v
	}

	info, err := s.supervisor.Start(ctx, bridge.SessionConfig{
		SessionID: req.SessionId,
		ProjectID: req.ProjectId,
		RepoPath:  req.RepoPath,
		Options:   opts,
	})
	if err != nil {
		s.logger.Error("start session failed", "session_id", req.SessionId, "error", err)
		return nil, status.Errorf(codes.Internal, "start session: %v", err)
	}

	s.logger.Info("session started",
		"session_id", info.SessionID,
		"project_id", info.ProjectID,
		"provider", info.Provider,
		"caller", claims.Subject,
	)

	return &bridgev1.StartSessionResponse{
		SessionId: info.SessionID,
		Status:    mapState(info.State),
		CreatedAt: timestamppb.New(info.CreatedAt),
	}, nil
}

func (s *BridgeServer) StopSession(ctx context.Context, req *bridgev1.StopSessionRequest) (*bridgev1.StopSessionResponse, error) {
	if _, ok := auth.ClaimsFromContext(ctx); !ok {
		return nil, status.Error(codes.Unauthenticated, "missing claims")
	}

	if req.SessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}

	if err := s.supervisor.Stop(req.SessionId, req.Force); err != nil {
		return nil, status.Errorf(codes.Internal, "stop session: %v", err)
	}

	s.logger.Info("session stopped", "session_id", req.SessionId)
	return &bridgev1.StopSessionResponse{
		Status: bridgev1.SessionStatus_SESSION_STATUS_STOPPED,
	}, nil
}

func (s *BridgeServer) GetSession(ctx context.Context, req *bridgev1.GetSessionRequest) (*bridgev1.GetSessionResponse, error) {
	if _, ok := auth.ClaimsFromContext(ctx); !ok {
		return nil, status.Error(codes.Unauthenticated, "missing claims")
	}

	if req.SessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}

	info, err := s.supervisor.Get(req.SessionId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "%v", err)
	}

	return sessionInfoToProto(info), nil
}

func (s *BridgeServer) ListSessions(ctx context.Context, req *bridgev1.ListSessionsRequest) (*bridgev1.ListSessionsResponse, error) {
	if _, ok := auth.ClaimsFromContext(ctx); !ok {
		return nil, status.Error(codes.Unauthenticated, "missing claims")
	}

	sessions := s.supervisor.List(req.ProjectId)
	resp := &bridgev1.ListSessionsResponse{
		Sessions: make([]*bridgev1.GetSessionResponse, 0, len(sessions)),
	}
	for i := range sessions {
		resp.Sessions = append(resp.Sessions, sessionInfoToProto(&sessions[i]))
	}
	return resp, nil
}

func (s *BridgeServer) SendInput(ctx context.Context, req *bridgev1.SendInputRequest) (*bridgev1.SendInputResponse, error) {
	if _, ok := auth.ClaimsFromContext(ctx); !ok {
		return nil, status.Error(codes.Unauthenticated, "missing claims")
	}

	if req.SessionId == "" || req.Text == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id and text are required")
	}

	seq, err := s.supervisor.Send(req.SessionId, req.Text)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "send input: %v", err)
	}

	return &bridgev1.SendInputResponse{
		Accepted: true,
		Seq:      seq,
	}, nil
}

func (s *BridgeServer) StreamEvents(req *bridgev1.StreamEventsRequest, stream bridgev1.BridgeService_StreamEventsServer) error {
	claims, ok := auth.ClaimsFromContext(stream.Context())
	if !ok {
		return status.Error(codes.Unauthenticated, "missing claims")
	}
	_ = claims

	if req.SessionId == "" {
		return status.Error(codes.InvalidArgument, "session_id is required")
	}

	buf, err := s.supervisor.EventBuffer(req.SessionId)
	if err != nil {
		return status.Errorf(codes.NotFound, "%v", err)
	}

	// Replay buffered events
	replayed := buf.After(req.AfterSeq)
	for _, se := range replayed {
		if err := stream.Send(seqEventToProto(se)); err != nil {
			return err
		}
	}

	// Switch to live streaming
	sub := buf.Subscribe()
	defer buf.Unsubscribe(sub)

	lastSeq := req.AfterSeq
	if len(replayed) > 0 {
		lastSeq = replayed[len(replayed)-1].Seq
	}

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case se, ok := <-sub:
			if !ok {
				return nil // channel closed
			}
			if se.Seq <= lastSeq {
				continue // skip duplicates from replay
			}
			lastSeq = se.Seq
			if err := stream.Send(seqEventToProto(se)); err != nil {
				return err
			}
		}
	}
}

func (s *BridgeServer) Health(ctx context.Context, req *bridgev1.HealthRequest) (*bridgev1.HealthResponse, error) {
	results := s.registry.HealthAll(ctx)

	providers := make([]*bridgev1.ProviderHealth, 0, len(results))
	for id, err := range results {
		ph := &bridgev1.ProviderHealth{
			Provider:  id,
			Available: err == nil,
		}
		if err != nil {
			ph.Error = err.Error()
		}
		providers = append(providers, ph)
	}

	return &bridgev1.HealthResponse{
		Status:    "serving",
		Providers: providers,
	}, nil
}

func (s *BridgeServer) ListProviders(ctx context.Context, req *bridgev1.ListProvidersRequest) (*bridgev1.ListProvidersResponse, error) {
	ids := s.registry.List()
	results := s.registry.HealthAll(ctx)

	providers := make([]*bridgev1.ProviderInfo, 0, len(ids))
	for _, id := range ids {
		pi := &bridgev1.ProviderInfo{
			Provider:  id,
			Available: results[id] == nil,
		}
		providers = append(providers, pi)
	}

	return &bridgev1.ListProvidersResponse{Providers: providers}, nil
}

// --- helpers ---

func sessionInfoToProto(info *bridge.SessionInfo) *bridgev1.GetSessionResponse {
	resp := &bridgev1.GetSessionResponse{
		SessionId: info.SessionID,
		ProjectId: info.ProjectID,
		Provider:  info.Provider,
		Status:    mapState(info.State),
		CreatedAt: timestamppb.New(info.CreatedAt),
		Error:     info.Error,
	}
	if !info.StoppedAt.IsZero() {
		resp.StoppedAt = timestamppb.New(info.StoppedAt)
	}
	return resp
}

func mapState(s bridge.SessionState) bridgev1.SessionStatus {
	switch s {
	case bridge.SessionStateStarting:
		return bridgev1.SessionStatus_SESSION_STATUS_STARTING
	case bridge.SessionStateRunning:
		return bridgev1.SessionStatus_SESSION_STATUS_RUNNING
	case bridge.SessionStateStopping:
		return bridgev1.SessionStatus_SESSION_STATUS_STOPPING
	case bridge.SessionStateStopped:
		return bridgev1.SessionStatus_SESSION_STATUS_STOPPED
	case bridge.SessionStateFailed:
		return bridgev1.SessionStatus_SESSION_STATUS_FAILED
	default:
		return bridgev1.SessionStatus_SESSION_STATUS_UNSPECIFIED
	}
}

func mapEventType(t bridge.EventType) bridgev1.EventType {
	switch t {
	case bridge.EventTypeSessionStarted:
		return bridgev1.EventType_EVENT_TYPE_SESSION_STARTED
	case bridge.EventTypeSessionStopped:
		return bridgev1.EventType_EVENT_TYPE_SESSION_STOPPED
	case bridge.EventTypeSessionFailed:
		return bridgev1.EventType_EVENT_TYPE_SESSION_FAILED
	case bridge.EventTypeStdout:
		return bridgev1.EventType_EVENT_TYPE_STDOUT
	case bridge.EventTypeStderr:
		return bridgev1.EventType_EVENT_TYPE_STDERR
	case bridge.EventTypeInputReceived:
		return bridgev1.EventType_EVENT_TYPE_INPUT_RECEIVED
	case bridge.EventTypeBufferOverflow:
		return bridgev1.EventType_EVENT_TYPE_BUFFER_OVERFLOW
	default:
		return bridgev1.EventType_EVENT_TYPE_UNSPECIFIED
	}
}

func seqEventToProto(se bridge.SequencedEvent) *bridgev1.SessionEvent {
	return &bridgev1.SessionEvent{
		Seq:       se.Seq,
		Timestamp: timestamppb.New(se.Timestamp),
		SessionId: se.SessionID,
		ProjectId: se.ProjectID,
		Provider:  se.Provider,
		Type:      mapEventType(se.Type),
		Stream:    se.Stream,
		Text:      se.Text,
		Done:      se.Done,
		Error:     se.Error,
	}
}

