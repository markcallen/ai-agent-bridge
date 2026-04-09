package server

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/internal/auth"
	"github.com/markcallen/ai-agent-bridge/internal/bridge"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type serverTestProvider struct {
	id        string
	healthErr error
	version   string
}

func (p *serverTestProvider) ID() string                    { return p.id }
func (p *serverTestProvider) Binary() string                { return "/bin/true" }
func (p *serverTestProvider) PromptPattern() *regexp.Regexp { return nil }
func (p *serverTestProvider) StartupTimeout() time.Duration { return time.Second }
func (p *serverTestProvider) StopGrace() time.Duration      { return time.Second }
func (p *serverTestProvider) BuildCommand(context.Context, bridge.SessionConfig) (*exec.Cmd, error) {
	if p.id == "cat" {
		return exec.Command("/bin/cat"), nil
	}
	return exec.Command("/bin/true"), nil
}
func (p *serverTestProvider) ValidateStartup(context.Context) error { return nil }
func (p *serverTestProvider) Health(context.Context) error          { return p.healthErr }
func (p *serverTestProvider) Version(context.Context) (string, error) {
	return p.version, nil
}

func TestValidationHelpers(t *testing.T) {
	if err := validateStringField("field", "ok", 10, false); err != nil {
		t.Fatalf("validateStringField: %v", err)
	}
	if err := validateStringField("field", "", 10, false); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty string code=%v want %v", status.Code(err), codes.InvalidArgument)
	}
	if err := validateOptionalStringField("field", "", 10, false); err != nil {
		t.Fatalf("validateOptionalStringField empty: %v", err)
	}
	if err := validateByteField("data", []byte("x"), 1); err != nil {
		t.Fatalf("validateByteField: %v", err)
	}
	if err := validateUUIDField("session_id", "not-a-uuid"); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("validateUUIDField code=%v want %v", status.Code(err), codes.InvalidArgument)
	}
}

func TestRateLimiters(t *testing.T) {
	now := time.Now()
	bucket := newTokenBucket(1, 1, now)
	if !bucket.allow(now) {
		t.Fatal("first allow was false")
	}
	if bucket.allow(now) {
		t.Fatal("second allow was true without refill")
	}
	if !bucket.allow(now.Add(1100 * time.Millisecond)) {
		t.Fatal("allow after refill was false")
	}

	limiter := newKeyedLimiter(1, 1)
	limiter.ttl = time.Millisecond
	if !limiter.allow("client-a") {
		t.Fatal("keyed limiter first allow was false")
	}
	limiter.mu.Lock()
	limiter.buckets["client-a"].lastSeen = time.Now().Add(-time.Hour)
	limiter.cleanupLocked(time.Now())
	_, exists := limiter.buckets["client-a"]
	limiter.mu.Unlock()
	if exists {
		t.Fatal("stale bucket still existed after cleanup")
	}
}

func TestBridgeHelpersAndProviderResponses(t *testing.T) {
	registry := bridge.NewRegistry()
	if err := registry.Register(&serverTestProvider{id: "healthy", version: "v1.2.3"}); err != nil {
		t.Fatalf("Register healthy: %v", err)
	}
	if err := registry.Register(&serverTestProvider{id: "broken", healthErr: errors.New("down")}); err != nil {
		t.Fatalf("Register broken: %v", err)
	}

	s := New(nil, registry, slog.Default(), RateLimitConfig{}, "test-instance", nil)
	health, err := s.Health(context.Background(), &bridgev1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.Status != "serving" || len(health.Providers) != 2 {
		t.Fatalf("Health=%+v", health)
	}

	providers, err := s.ListProviders(context.Background(), &bridgev1.ListProvidersRequest{})
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(providers.Providers) != 2 {
		t.Fatalf("providers len=%d want 2", len(providers.Providers))
	}

	ctx := auth.ContextWithClaims(context.Background(), &auth.BridgeClaims{ProjectID: "project-a"})
	claims, err := mustClaims(ctx)
	if err != nil {
		t.Fatalf("mustClaims: %v", err)
	}
	if err := authorizeProject(claims, "project-a"); err != nil {
		t.Fatalf("authorizeProject: %v", err)
	}
	if err := authorizeProject(claims, "project-b"); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("authorizeProject mismatch code=%v want %v", status.Code(err), codes.PermissionDenied)
	}
	if _, err := mustClaims(context.Background()); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("mustClaims missing code=%v want %v", status.Code(err), codes.Unauthenticated)
	}

	info := &bridge.SessionInfo{
		SessionID:        "session-a",
		ProjectID:        "project-a",
		Provider:         "healthy",
		State:            bridge.SessionStateAttached,
		CreatedAt:        time.Unix(10, 0),
		StoppedAt:        time.Unix(20, 0),
		Error:            "boom",
		Attached:         true,
		AttachedClientID: "client-a",
		ExitRecorded:     true,
		ExitCode:         9,
		OldestSeq:        1,
		LastSeq:          2,
		Cols:             80,
		Rows:             24,
	}
	resp := sessionInfoToProto(info)
	if resp.GetSessionId() != "session-a" || resp.GetStatus() != bridgev1.SessionStatus_SESSION_STATUS_ATTACHED {
		t.Fatalf("sessionInfoToProto=%+v", resp)
	}

	chunk := chunkToProto("session-a", bridge.OutputChunk{
		Seq:       7,
		Timestamp: time.Unix(30, 0),
		Payload:   []byte("hello"),
	}, true)
	if chunk.GetSeq() != 7 || !chunk.GetReplay() {
		t.Fatalf("chunkToProto=%+v", chunk)
	}
}

func TestMapBridgeErrorAndState(t *testing.T) {
	cases := []struct {
		err  error
		code codes.Code
	}{
		{err: bridge.ErrInvalidArgument, code: codes.InvalidArgument},
		{err: bridge.ErrSessionNotFound, code: codes.NotFound},
		{err: bridge.ErrSessionAlreadyExists, code: codes.AlreadyExists},
		{err: bridge.ErrSessionAlreadyAttached, code: codes.ResourceExhausted},
		{err: bridge.ErrClientMismatch, code: codes.PermissionDenied},
		{err: bridge.ErrProviderUnavailable, code: codes.Unavailable},
		{err: bridge.ErrSessionRecoveryUnavailable, code: codes.Unavailable},
		{err: bridge.ErrSessionLimitReached, code: codes.ResourceExhausted},
		{err: errors.New("boom"), code: codes.Internal},
	}
	for _, tc := range cases {
		if got := status.Code(mapBridgeError(tc.err, "op")); got != tc.code {
			t.Fatalf("mapBridgeError(%v) code=%v want %v", tc.err, got, tc.code)
		}
	}

	if got := mapState(bridge.SessionStateFailed); got != bridgev1.SessionStatus_SESSION_STATUS_FAILED {
		t.Fatalf("mapState failed=%v", got)
	}
	if got := mapState(bridge.SessionState(999)); got != bridgev1.SessionStatus_SESSION_STATUS_UNSPECIFIED {
		t.Fatalf("mapState unknown=%v", got)
	}

	errText := mapBridgeError(bridge.ErrProviderUnavailable, "list")
	if !strings.Contains(errText.Error(), "list:") {
		t.Fatalf("mapBridgeError text=%q want op prefix", errText.Error())
	}
}
