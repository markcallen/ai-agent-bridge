// Package bridgelib provides an embeddable bridge that can be integrated
// into an existing Go application without running a separate gRPC server.
package bridgelib

import (
	"context"
	"fmt"
	"time"

	"github.com/markcallen/ai-agent-bridge/internal/bridge"
	"github.com/markcallen/ai-agent-bridge/internal/provider"
)

// ProviderConfig configures a single provider in the embedded bridge.
// If ID is "claude" and Binary is empty, the default claude CLI configuration is used.
type ProviderConfig struct {
	ID             string
	Binary         string
	Args           []string
	StartupTimeout time.Duration
	StopGrace      time.Duration
	StreamJSON     bool
	UsePTY         bool
	PromptPattern  string
}

// Config holds configuration for an embedded Bridge.
type Config struct {
	// Providers lists provider configs to register.
	// If empty, a single "claude" provider with default settings is registered.
	Providers []ProviderConfig

	// AllowedPaths restricts which repo paths may be used.
	// If empty, all paths are allowed.
	AllowedPaths []string

	// MaxSessions is the global concurrent session limit. Default: 20.
	MaxSessions int

	// MaxSessionsPerProject is the per-project limit. Default: 5.
	MaxSessionsPerProject int

	// IdleTimeout before an idle session is stopped. Default: 30 minutes.
	IdleTimeout time.Duration

	// EventBufferSize is the ring buffer capacity per session. Default: 10000.
	EventBufferSize int
}

// SessionInfo describes a bridge session.
type SessionInfo struct {
	SessionID string
	ProjectID string
	Provider  string
	// State: 1=Starting 2=Running 3=Stopping 4=Stopped 5=Failed
	State     int
	CreatedAt time.Time
	StoppedAt time.Time
	Error     string
}

// SequencedEvent is a bridge event paired with its monotonic sequence number.
type SequencedEvent struct {
	Seq       uint64
	SessionID string
	ProjectID string
	Provider  string
	// TypeName is the human-readable event type (e.g. "stdout", "agent_ready").
	TypeName  string
	Stream    string
	Text      string
	Done      bool
	Error     string
	Timestamp time.Time
}

// Bridge is an embeddable, gRPC-free bridge instance.
type Bridge struct {
	supervisor *bridge.Supervisor
	registry   *bridge.Registry
}

// New creates an embedded Bridge with the given configuration.
func New(cfg Config) (*Bridge, error) {
	registry := bridge.NewRegistry()

	providers := cfg.Providers
	if len(providers) == 0 {
		providers = []ProviderConfig{{ID: "claude"}}
	}

	for _, pc := range providers {
		var prov bridge.Provider
		if pc.Binary == "" && pc.ID == "claude" {
			// Use the well-known claude CLI defaults.
			prov = provider.NewClaudeProvider()
		} else {
			if pc.StartupTimeout == 0 {
				pc.StartupTimeout = 30 * time.Second
			}
			if pc.StopGrace == 0 {
				pc.StopGrace = 10 * time.Second
			}
			prov = provider.NewStdioProvider(provider.StdioConfig{
				ProviderID:     pc.ID,
				Binary:         pc.Binary,
				DefaultArgs:    pc.Args,
				StartupTimeout: pc.StartupTimeout,
				StopGrace:      pc.StopGrace,
				StreamJSON:     pc.StreamJSON,
				UsePTY:         pc.UsePTY,
				PromptPattern:  pc.PromptPattern,
			})
		}
		if err := registry.Register(prov); err != nil {
			return nil, fmt.Errorf("register provider %q: %w", pc.ID, err)
		}
	}

	maxSessions := cfg.MaxSessions
	if maxSessions == 0 {
		maxSessions = 20
	}
	maxPerProject := cfg.MaxSessionsPerProject
	if maxPerProject == 0 {
		maxPerProject = 5
	}
	idleTimeout := cfg.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = 30 * time.Minute
	}
	eventBufSize := cfg.EventBufferSize
	if eventBufSize == 0 {
		eventBufSize = 10000
	}

	policy := bridge.Policy{
		MaxPerProject: maxPerProject,
		MaxGlobal:     maxSessions,
		MaxInputBytes: 65536,
		AllowedPaths:  cfg.AllowedPaths,
	}

	supervisor := bridge.NewSupervisor(
		registry,
		policy,
		eventBufSize,
		bridge.DefaultSubscriberConfig(),
		idleTimeout,
	)

	return &Bridge{
		supervisor: supervisor,
		registry:   registry,
	}, nil
}

// StartSession starts a new agent session.
// opts is an optional map of additional agent options.
func (b *Bridge) StartSession(ctx context.Context, projectID, sessionID, repoPath, providerID string, opts map[string]string) (*SessionInfo, error) {
	if opts == nil {
		opts = make(map[string]string)
	}
	opts["provider"] = providerID

	info, err := b.supervisor.Start(ctx, bridge.SessionConfig{
		ProjectID: projectID,
		SessionID: sessionID,
		RepoPath:  repoPath,
		Options:   opts,
	})
	if err != nil {
		return nil, err
	}
	return toSessionInfo(info), nil
}

// Send writes input text to a running session and returns the sequence number.
func (b *Bridge) Send(sessionID, text string) (uint64, error) {
	return b.supervisor.Send(sessionID, text)
}

// Stop terminates a session. If force is true, the agent is killed immediately.
func (b *Bridge) Stop(sessionID string, force bool) error {
	return b.supervisor.Stop(sessionID, force)
}

// Get returns info about a session.
func (b *Bridge) Get(sessionID string) (*SessionInfo, error) {
	info, err := b.supervisor.Get(sessionID)
	if err != nil {
		return nil, err
	}
	return toSessionInfo(info), nil
}

// List returns all sessions, optionally filtered by projectID.
func (b *Bridge) List(projectID string) []SessionInfo {
	infos := b.supervisor.List(projectID)
	result := make([]SessionInfo, len(infos))
	for i, info := range infos {
		result[i] = *toSessionInfo(&info)
	}
	return result
}

// StreamEvents attaches subscriberID to sessionID and returns a channel of events.
// Replay events (seq > afterSeq) are delivered first, followed by live events.
// Cancel ctx to stop streaming and release resources.
func (b *Bridge) StreamEvents(ctx context.Context, sessionID, subscriberID string, afterSeq uint64) (<-chan SequencedEvent, error) {
	subMgr, err := b.supervisor.SubscriberManager(sessionID)
	if err != nil {
		return nil, err
	}

	res, err := subMgr.Attach(subscriberID, afterSeq)
	if err != nil {
		return nil, err
	}

	out := make(chan SequencedEvent, 64)
	go func() {
		defer close(out)
		defer subMgr.Detach(subscriberID, res.Live)

		// Deliver replay events first.
		for _, se := range res.Replay {
			select {
			case out <- toSequencedEvent(se):
			case <-ctx.Done():
				return
			}
		}

		// Then stream live events.
		for {
			select {
			case se, ok := <-res.Live:
				if !ok {
					return
				}
				select {
				case out <- toSequencedEvent(se):
					subMgr.Ack(subscriberID, se.Seq)
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

// Health returns a map of provider ID to health error (nil means healthy).
func (b *Bridge) Health(ctx context.Context) map[string]error {
	return b.registry.HealthAll(ctx)
}

// ListProviders returns all registered provider IDs.
func (b *Bridge) ListProviders() []string {
	return b.registry.List()
}

// Close shuts down all sessions and background goroutines.
func (b *Bridge) Close() {
	b.supervisor.Close()
}

// --- internal helpers ---

func toSessionInfo(info *bridge.SessionInfo) *SessionInfo {
	return &SessionInfo{
		SessionID: info.SessionID,
		ProjectID: info.ProjectID,
		Provider:  info.Provider,
		State:     int(info.State),
		CreatedAt: info.CreatedAt,
		StoppedAt: info.StoppedAt,
		Error:     info.Error,
	}
}

func toSequencedEvent(se bridge.SequencedEvent) SequencedEvent {
	return SequencedEvent{
		Seq:       se.Seq,
		SessionID: se.SessionID,
		ProjectID: se.ProjectID,
		Provider:  se.Provider,
		TypeName:  eventTypeName(se.Type),
		Stream:    se.Stream,
		Text:      se.Text,
		Done:      se.Done,
		Error:     se.Error,
		Timestamp: se.Timestamp,
	}
}

func eventTypeName(t bridge.EventType) string {
	switch t {
	case bridge.EventTypeSessionStarted:
		return "session_started"
	case bridge.EventTypeSessionStopped:
		return "session_stopped"
	case bridge.EventTypeSessionFailed:
		return "session_failed"
	case bridge.EventTypeStdout:
		return "stdout"
	case bridge.EventTypeStderr:
		return "stderr"
	case bridge.EventTypeInputReceived:
		return "input_received"
	case bridge.EventTypeBufferOverflow:
		return "buffer_overflow"
	case bridge.EventTypeAgentReady:
		return "agent_ready"
	case bridge.EventTypeResponseComplete:
		return "response_complete"
	default:
		return "unspecified"
	}
}
