package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/internal/auth"
	"github.com/markcallen/ai-agent-bridge/internal/bridge"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

type BridgeServer struct {
	bridgev1.UnimplementedBridgeServiceServer

	supervisor       *bridge.Supervisor
	registry         *bridge.Registry
	logger           *slog.Logger
	globalRL         *keyedLimiter
	startRL          *keyedLimiter
	writeRL          *keyedLimiter
	serverInstanceID string
}

type RateLimitConfig struct {
	GlobalRPS                  float64
	GlobalBurst                int
	StartSessionPerClientRPS   float64
	StartSessionPerClientBurst int
	SendInputPerSessionRPS     float64
	SendInputPerSessionBurst   int
}

func New(supervisor *bridge.Supervisor, registry *bridge.Registry, logger *slog.Logger, rl RateLimitConfig, serverInstanceID string) *BridgeServer {
	return &BridgeServer{
		supervisor:       supervisor,
		registry:         registry,
		logger:           logger,
		globalRL:         newKeyedLimiter(rl.GlobalRPS, rl.GlobalBurst),
		startRL:          newKeyedLimiter(rl.StartSessionPerClientRPS, rl.StartSessionPerClientBurst),
		writeRL:          newKeyedLimiter(rl.SendInputPerSessionRPS, rl.SendInputPerSessionBurst),
		serverInstanceID: serverInstanceID,
	}
}

func (s *BridgeServer) StartSession(ctx context.Context, req *bridgev1.StartSessionRequest) (*bridgev1.StartSessionResponse, error) {
	if !s.globalRL.allow("global") {
		return nil, status.Error(codes.ResourceExhausted, "global RPC rate limit exceeded")
	}
	claims, err := mustClaims(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateStringField("project_id", req.ProjectId, maxProjectIDLen, false); err != nil {
		return nil, err
	}
	if err := validateUUIDField("session_id", req.SessionId); err != nil {
		return nil, err
	}
	if err := validateStringField("repo_path", req.RepoPath, maxRepoPathLen, false); err != nil {
		return nil, err
	}
	if err := validateStringField("provider", req.Provider, maxProviderLen, false); err != nil {
		return nil, err
	}
	if err := authorizeProject(claims, req.ProjectId); err != nil {
		return nil, err
	}

	clientID := claims.Subject
	if clientID == "" {
		clientID = claims.ProjectID
	}
	if !s.startRL.allow(clientID) {
		return nil, status.Error(codes.ResourceExhausted, "start session rate limit exceeded for client")
	}

	opts := map[string]string{"provider": req.Provider}
	for k, v := range req.AgentOpts {
		opts[k] = v
	}

	info, err := s.supervisor.Start(ctx, bridge.SessionConfig{
		SessionID:   req.SessionId,
		ProjectID:   req.ProjectId,
		RepoPath:    req.RepoPath,
		Options:     opts,
		InitialCols: req.InitialCols,
		InitialRows: req.InitialRows,
	})
	if err != nil {
		return nil, mapBridgeError(err, "start session")
	}
	return &bridgev1.StartSessionResponse{
		SessionId: info.SessionID,
		Status:    mapState(info.State),
		CreatedAt: timestamppb.New(info.CreatedAt),
	}, nil
}

func (s *BridgeServer) StopSession(ctx context.Context, req *bridgev1.StopSessionRequest) (*bridgev1.StopSessionResponse, error) {
	if !s.globalRL.allow("global") {
		return nil, status.Error(codes.ResourceExhausted, "global RPC rate limit exceeded")
	}
	claims, err := mustClaims(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateUUIDField("session_id", req.SessionId); err != nil {
		return nil, err
	}
	if err := s.authorizeSession(claims, req.SessionId); err != nil {
		return nil, err
	}
	if err := s.supervisor.Stop(req.SessionId, req.Force); err != nil {
		return nil, mapBridgeError(err, "stop session")
	}
	return &bridgev1.StopSessionResponse{Status: bridgev1.SessionStatus_SESSION_STATUS_STOPPING}, nil
}

func (s *BridgeServer) GetSession(ctx context.Context, req *bridgev1.GetSessionRequest) (*bridgev1.GetSessionResponse, error) {
	if !s.globalRL.allow("global") {
		return nil, status.Error(codes.ResourceExhausted, "global RPC rate limit exceeded")
	}
	claims, err := mustClaims(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateUUIDField("session_id", req.SessionId); err != nil {
		return nil, err
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
	if !s.globalRL.allow("global") {
		return nil, status.Error(codes.ResourceExhausted, "global RPC rate limit exceeded")
	}
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
	items := s.supervisor.List(projectID)
	resp := &bridgev1.ListSessionsResponse{
		Sessions: make([]*bridgev1.GetSessionResponse, 0, len(items)),
	}
	for i := range items {
		info := items[i]
		resp.Sessions = append(resp.Sessions, sessionInfoToProto(&info))
	}
	return resp, nil
}

func (s *BridgeServer) AttachSession(req *bridgev1.AttachSessionRequest, stream bridgev1.BridgeService_AttachSessionServer) error {
	if !s.globalRL.allow("global") {
		return status.Error(codes.ResourceExhausted, "global RPC rate limit exceeded")
	}
	claims, err := mustClaims(stream.Context())
	if err != nil {
		return err
	}
	if err := validateUUIDField("session_id", req.SessionId); err != nil {
		return err
	}
	if err := validateOptionalStringField("client_id", req.ClientId, maxSessionIDLen, false); err != nil {
		return err
	}
	if err := s.authorizeSession(claims, req.SessionId); err != nil {
		return err
	}
	clientID := req.ClientId
	if clientID == "" {
		clientID = generateID()
	}
	state, err := s.supervisor.Attach(req.SessionId, clientID, req.AfterSeq)
	if err != nil {
		return mapBridgeError(err, "attach session")
	}
	defer func() {
		_ = s.supervisor.Detach(req.SessionId, clientID)
	}()

	if err := stream.Send(&bridgev1.AttachSessionEvent{
		Type:         bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ATTACHED,
		SessionId:    req.SessionId,
		OldestSeq:    state.OldestSeq,
		LastSeq:      state.LastSeq,
		ExitRecorded: state.ExitRecorded,
		ExitCode:     int32(state.ExitCode),
		Cols:         state.Cols,
		Rows:         state.Rows,
	}); err != nil {
		return err
	}
	if state.ReplayGap {
		if err := stream.Send(&bridgev1.AttachSessionEvent{
			Type:      bridgev1.AttachEventType_ATTACH_EVENT_TYPE_REPLAY_GAP,
			SessionId: req.SessionId,
			OldestSeq: state.OldestSeq,
			LastSeq:   state.LastSeq,
		}); err != nil {
			return err
		}
	}
	lastSeq := req.AfterSeq
	for _, chunk := range state.Replay {
		if err := stream.Send(chunkToProto(req.SessionId, chunk, true)); err != nil {
			return err
		}
		lastSeq = chunk.Seq
	}
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case chunk, ok := <-state.Live:
			if !ok {
				return nil
			}
			if chunk.Seq <= lastSeq {
				continue
			}
			lastSeq = chunk.Seq
			if err := stream.Send(chunkToProto(req.SessionId, chunk, false)); err != nil {
				return err
			}
		}
	}
}

func (s *BridgeServer) WriteInput(ctx context.Context, req *bridgev1.WriteInputRequest) (*bridgev1.WriteInputResponse, error) {
	if !s.globalRL.allow("global") {
		return nil, status.Error(codes.ResourceExhausted, "global RPC rate limit exceeded")
	}
	claims, err := mustClaims(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateUUIDField("session_id", req.SessionId); err != nil {
		return nil, err
	}
	if err := validateStringField("client_id", req.ClientId, maxSessionIDLen, false); err != nil {
		return nil, err
	}
	if err := validateByteField("data", req.Data, 1<<20); err != nil {
		return nil, err
	}
	if !s.writeRL.allow(req.SessionId) {
		return nil, status.Error(codes.ResourceExhausted, "write input rate limit exceeded for session")
	}
	if err := s.authorizeSession(claims, req.SessionId); err != nil {
		return nil, err
	}
	n, err := s.supervisor.WriteInput(req.SessionId, req.ClientId, req.Data)
	if err != nil {
		return nil, mapBridgeError(err, "write input")
	}
	return &bridgev1.WriteInputResponse{Accepted: true, BytesWritten: uint32(n)}, nil
}

func (s *BridgeServer) ResizeSession(ctx context.Context, req *bridgev1.ResizeSessionRequest) (*bridgev1.ResizeSessionResponse, error) {
	if !s.globalRL.allow("global") {
		return nil, status.Error(codes.ResourceExhausted, "global RPC rate limit exceeded")
	}
	claims, err := mustClaims(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateUUIDField("session_id", req.SessionId); err != nil {
		return nil, err
	}
	if err := validateStringField("client_id", req.ClientId, maxSessionIDLen, false); err != nil {
		return nil, err
	}
	if req.Cols == 0 || req.Rows == 0 {
		return nil, status.Error(codes.InvalidArgument, "cols and rows must be > 0")
	}
	if err := s.authorizeSession(claims, req.SessionId); err != nil {
		return nil, err
	}
	if err := s.supervisor.Resize(req.SessionId, req.ClientId, req.Cols, req.Rows); err != nil {
		return nil, mapBridgeError(err, "resize session")
	}
	return &bridgev1.ResizeSessionResponse{Applied: true}, nil
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
	case errors.Is(err, bridge.ErrSessionAlreadyAttached), errors.Is(err, bridge.ErrInputTooLarge):
		return status.Errorf(codes.ResourceExhausted, "%s: %v", op, err)
	case errors.Is(err, bridge.ErrClientNotAttached), errors.Is(err, bridge.ErrClientMismatch):
		return status.Errorf(codes.PermissionDenied, "%s: %v", op, err)
	case errors.Is(err, bridge.ErrProviderUnavailable):
		return status.Errorf(codes.Unavailable, "%s: %v", op, err)
	case errors.Is(err, bridge.ErrSessionLimitReached):
		return status.Errorf(codes.ResourceExhausted, "%s: %v", op, err)
	default:
		return status.Errorf(codes.Internal, "%s: %v", op, err)
	}
}

func (s *BridgeServer) Health(ctx context.Context, req *bridgev1.HealthRequest) (*bridgev1.HealthResponse, error) {
	results := s.registry.HealthAll(ctx)
	providers := make([]*bridgev1.ProviderHealth, 0, len(results))
	for id, err := range results {
		item := &bridgev1.ProviderHealth{Provider: id, Available: err == nil}
		if err != nil {
			item.Error = err.Error()
		}
		providers = append(providers, item)
	}
	return &bridgev1.HealthResponse{
		Status:           "serving",
		Providers:        providers,
		ServerInstanceId: s.serverInstanceID,
	}, nil
}

func (s *BridgeServer) ListProviders(ctx context.Context, req *bridgev1.ListProvidersRequest) (*bridgev1.ListProvidersResponse, error) {
	ids := s.registry.List()
	results := s.registry.HealthAll(ctx)
	items := make([]*bridgev1.ProviderInfo, 0, len(ids))
	for _, id := range ids {
		var version string
		if p, err := s.registry.Get(id); err == nil && results[id] == nil {
			version, _ = p.Version(ctx)
		}
		items = append(items, &bridgev1.ProviderInfo{
			Provider:  id,
			Available: results[id] == nil,
			Binary:    "",
			Version:   version,
		})
	}
	return &bridgev1.ListProvidersResponse{Providers: items}, nil
}

func sessionInfoToProto(info *bridge.SessionInfo) *bridgev1.GetSessionResponse {
	resp := &bridgev1.GetSessionResponse{
		SessionId:        info.SessionID,
		ProjectId:        info.ProjectID,
		Provider:         info.Provider,
		Status:           mapState(info.State),
		CreatedAt:        timestamppb.New(info.CreatedAt),
		Error:            info.Error,
		Attached:         info.Attached,
		AttachedClientId: info.AttachedClientID,
		ExitRecorded:     info.ExitRecorded,
		ExitCode:         int32(info.ExitCode),
		OldestSeq:        info.OldestSeq,
		LastSeq:          info.LastSeq,
		Cols:             info.Cols,
		Rows:             info.Rows,
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
	case bridge.SessionStateAttached:
		return bridgev1.SessionStatus_SESSION_STATUS_ATTACHED
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

func chunkToProto(sessionID string, chunk bridge.OutputChunk, replay bool) *bridgev1.AttachSessionEvent {
	return &bridgev1.AttachSessionEvent{
		Type:      bridgev1.AttachEventType_ATTACH_EVENT_TYPE_OUTPUT,
		Seq:       chunk.Seq,
		Timestamp: timestamppb.New(chunk.Timestamp),
		SessionId: sessionID,
		Payload:   chunk.Payload,
		Replay:    replay,
	}
}
