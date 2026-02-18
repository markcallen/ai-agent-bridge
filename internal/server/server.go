package server

import (
	"context"
	"errors"
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
	claims, err := mustClaims(ctx)
	if err != nil {
		return nil, err
	}

	if req.ProjectId == "" || req.SessionId == "" || req.RepoPath == "" || req.Provider == "" {
		return nil, status.Error(codes.InvalidArgument, "project_id, session_id, repo_path, and provider are required")
	}

	// Authorization: JWT project_id must match request
	if err := authorizeProject(claims, req.ProjectId); err != nil {
		return nil, err
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
		return nil, mapBridgeError(err, "start session")
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
	claims, err := mustClaims(ctx)
	if err != nil {
		return nil, err
	}

	if req.SessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}

	if err := s.authorizeSession(claims, req.SessionId); err != nil {
		return nil, err
	}

	if err := s.supervisor.Stop(req.SessionId, req.Force); err != nil {
		return nil, mapBridgeError(err, "stop session")
	}

	s.logger.Info("session stopped", "session_id", req.SessionId)
	return &bridgev1.StopSessionResponse{
		Status: bridgev1.SessionStatus_SESSION_STATUS_STOPPED,
	}, nil
}

func (s *BridgeServer) GetSession(ctx context.Context, req *bridgev1.GetSessionRequest) (*bridgev1.GetSessionResponse, error) {
	claims, err := mustClaims(ctx)
	if err != nil {
		return nil, err
	}

	if req.SessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}

	if err := s.authorizeSession(claims, req.SessionId); err != nil {
		return nil, err
	}

	info, err := s.supervisor.Get(req.SessionId)
	if err != nil {
		return nil, mapBridgeError(err, "get session")
	}

	return sessionInfoToProto(info), nil
}

func (s *BridgeServer) ListSessions(ctx context.Context, req *bridgev1.ListSessionsRequest) (*bridgev1.ListSessionsResponse, error) {
	claims, err := mustClaims(ctx)
	if err != nil {
		return nil, err
	}
	projectID := req.ProjectId
	if claims.ProjectID != "" {
		if projectID != "" && projectID != claims.ProjectID {
			return nil, status.Errorf(codes.PermissionDenied, "token project_id %q does not match request %q", claims.ProjectID, projectID)
		}
		projectID = claims.ProjectID
	}
	sessions := s.supervisor.List(projectID)
	resp := &bridgev1.ListSessionsResponse{
		Sessions: make([]*bridgev1.GetSessionResponse, 0, len(sessions)),
	}
	for i := range sessions {
		resp.Sessions = append(resp.Sessions, sessionInfoToProto(&sessions[i]))
	}
	return resp, nil
}

func (s *BridgeServer) SendInput(ctx context.Context, req *bridgev1.SendInputRequest) (*bridgev1.SendInputResponse, error) {
	claims, err := mustClaims(ctx)
	if err != nil {
		return nil, err
	}

	if req.SessionId == "" || req.Text == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id and text are required")
	}

	if err := s.authorizeSession(claims, req.SessionId); err != nil {
		return nil, err
	}

	seq, err := s.supervisor.Send(req.SessionId, req.Text)
	if err != nil {
		return nil, mapBridgeError(err, "send input")
	}

	return &bridgev1.SendInputResponse{
		Accepted: true,
		Seq:      seq,
	}, nil
}

func (s *BridgeServer) StreamEvents(req *bridgev1.StreamEventsRequest, stream bridgev1.BridgeService_StreamEventsServer) error {
	claims, err := mustClaims(stream.Context())
	if err != nil {
		return err
	}

	if req.SessionId == "" {
		return status.Error(codes.InvalidArgument, "session_id is required")
	}

	if err := s.authorizeSession(claims, req.SessionId); err != nil {
		return err
	}

	buf, err := s.supervisor.EventBuffer(req.SessionId)
	if err != nil {
		return mapBridgeError(err, "stream events")
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

func mustClaims(ctx context.Context) (*auth.BridgeClaims, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing claims")
	}
	return claims, nil
}

func authorizeProject(claims *auth.BridgeClaims, projectID string) error {
	if claims.ProjectID != "" && claims.ProjectID != projectID {
		return status.Errorf(codes.PermissionDenied, "token project_id %q does not match request %q", claims.ProjectID, projectID)
	}
	return nil
}

func (s *BridgeServer) authorizeSession(claims *auth.BridgeClaims, sessionID string) error {
	info, err := s.supervisor.Get(sessionID)
	if err != nil {
		return mapBridgeError(err, "authorize session")
	}
	return authorizeProject(claims, info.ProjectID)
}

func mapBridgeError(err error, op string) error {
	switch {
	case errors.Is(err, bridge.ErrInvalidArgument), errors.Is(err, bridge.ErrSessionNotRunning):
		return status.Errorf(codes.InvalidArgument, "%s: %v", op, err)
	case errors.Is(err, bridge.ErrSessionNotFound):
		return status.Errorf(codes.NotFound, "%s: %v", op, err)
	case errors.Is(err, bridge.ErrSessionAlreadyExists):
		return status.Errorf(codes.AlreadyExists, "%s: %v", op, err)
	case errors.Is(err, bridge.ErrProviderUnavailable):
		return status.Errorf(codes.Unavailable, "%s: %v", op, err)
	case errors.Is(err, bridge.ErrSessionLimitReached), errors.Is(err, bridge.ErrInputTooLarge):
		return status.Errorf(codes.ResourceExhausted, "%s: %v", op, err)
	default:
		return status.Errorf(codes.Internal, "%s: %v", op, err)
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
