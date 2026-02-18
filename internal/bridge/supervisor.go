package bridge

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SessionState represents the lifecycle state of a session.
type SessionState int

const (
	SessionStateStarting SessionState = iota + 1
	SessionStateRunning
	SessionStateStopping
	SessionStateStopped
	SessionStateFailed
)

// SessionInfo holds metadata about a running session.
type SessionInfo struct {
	SessionID string
	ProjectID string
	Provider  string
	State     SessionState
	CreatedAt time.Time
	StoppedAt time.Time
	Error     string
}

// Supervisor manages the lifecycle of agent sessions.
type Supervisor struct {
	registry *Registry
	policy   Policy
	bufSize  int

	mu       sync.RWMutex
	sessions map[string]*managedSession // keyed by session_id
}

type managedSession struct {
	info   SessionInfo
	handle SessionHandle
	buf    *EventBuffer
	cancel context.CancelFunc
}

// NewSupervisor creates a new session supervisor.
func NewSupervisor(registry *Registry, policy Policy, eventBufSize int) *Supervisor {
	if eventBufSize <= 0 {
		eventBufSize = 10000
	}
	return &Supervisor{
		registry: registry,
		policy:   policy,
		bufSize:  eventBufSize,
		sessions: make(map[string]*managedSession),
	}
}

// Start creates and starts a new agent session.
func (s *Supervisor) Start(ctx context.Context, cfg SessionConfig) (*SessionInfo, error) {
	if cfg.SessionID == "" {
		return nil, fmt.Errorf("%w: session_id is required", ErrInvalidArgument)
	}
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("%w: project_id is required", ErrInvalidArgument)
	}
	if cfg.RepoPath == "" {
		return nil, fmt.Errorf("%w: repo_path is required", ErrInvalidArgument)
	}

	// Validate repo path
	if err := s.policy.ValidateRepoPath(cfg.RepoPath); err != nil {
		return nil, err
	}

	s.mu.Lock()
	// Check for duplicate
	if _, exists := s.sessions[cfg.SessionID]; exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrSessionAlreadyExists, cfg.SessionID)
	}

	// Check limits
	projectCount := 0
	globalCount := 0
	for _, ms := range s.sessions {
		if ms.info.State == SessionStateRunning || ms.info.State == SessionStateStarting {
			globalCount++
			if ms.info.ProjectID == cfg.ProjectID {
				projectCount++
			}
		}
	}
	if err := s.policy.CheckSessionLimits(projectCount, globalCount); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()

	// Look up provider
	provider, err := s.registry.Get(cfg.Options["provider"])
	if err != nil {
		return nil, err
	}
	if err := provider.Health(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	sessionCtx, cancel := context.WithCancel(context.Background())

	handle, err := provider.Start(sessionCtx, cfg)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start session: %w", err)
	}

	buf := NewEventBuffer(s.bufSize)
	now := time.Now().UTC()
	info := SessionInfo{
		SessionID: cfg.SessionID,
		ProjectID: cfg.ProjectID,
		Provider:  cfg.Options["provider"],
		State:     SessionStateRunning,
		CreatedAt: now,
	}

	ms := &managedSession{
		info:   info,
		handle: handle,
		buf:    buf,
		cancel: cancel,
	}

	s.mu.Lock()
	// Double-check no race
	if _, exists := s.sessions[cfg.SessionID]; exists {
		s.mu.Unlock()
		cancel()
		_ = provider.Stop(handle)
		return nil, fmt.Errorf("%w: %q", ErrSessionAlreadyExists, cfg.SessionID)
	}
	s.sessions[cfg.SessionID] = ms
	s.mu.Unlock()

	// Forward provider events to the event buffer
	go s.forwardEvents(cfg.SessionID, provider, handle, buf)

	return &info, nil
}

// Stop terminates a session.
func (s *Supervisor) Stop(sessionID string, force bool) error {
	s.mu.Lock()
	ms, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	if ms.info.State == SessionStateStopped || ms.info.State == SessionStateFailed {
		s.mu.Unlock()
		return nil
	}
	ms.info.State = SessionStateStopping
	s.mu.Unlock()

	provider, err := s.registry.Get(ms.info.Provider)
	if err != nil {
		return err
	}

	if err := provider.Stop(ms.handle); err != nil {
		return fmt.Errorf("stop session: %w", err)
	}

	s.mu.Lock()
	ms.info.State = SessionStateStopped
	ms.info.StoppedAt = time.Now().UTC()
	ms.cancel()
	s.mu.Unlock()

	return nil
}

// Send writes input to a session's agent.
func (s *Supervisor) Send(sessionID, text string) (uint64, error) {
	if err := s.policy.ValidateInput(text); err != nil {
		return 0, err
	}

	s.mu.RLock()
	ms, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	if ms.info.State != SessionStateRunning {
		return 0, fmt.Errorf("%w: %q (state=%d)", ErrSessionNotRunning, sessionID, ms.info.State)
	}

	provider, err := s.registry.Get(ms.info.Provider)
	if err != nil {
		return 0, err
	}

	if err := provider.Send(ms.handle, text); err != nil {
		return 0, err
	}

	seq := ms.buf.Append(Event{
		Timestamp: time.Now().UTC(),
		SessionID: sessionID,
		ProjectID: ms.info.ProjectID,
		Provider:  ms.info.Provider,
		Type:      EventTypeInputReceived,
		Stream:    "system",
		Text:      text,
	})

	return seq, nil
}

// Get returns info about a session.
func (s *Supervisor) Get(sessionID string) (*SessionInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ms, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	info := ms.info // copy
	return &info, nil
}

// List returns all sessions, optionally filtered by project.
func (s *Supervisor) List(projectID string) []SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []SessionInfo
	for _, ms := range s.sessions {
		if projectID == "" || ms.info.ProjectID == projectID {
			result = append(result, ms.info)
		}
	}
	return result
}

// EventBuffer returns the event buffer for a session.
func (s *Supervisor) EventBuffer(sessionID string) (*EventBuffer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ms, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	return ms.buf, nil
}

// ActiveCount returns the number of running sessions globally and for a project.
func (s *Supervisor) ActiveCount(projectID string) (project, global int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ms := range s.sessions {
		if ms.info.State == SessionStateRunning || ms.info.State == SessionStateStarting {
			global++
			if ms.info.ProjectID == projectID {
				project++
			}
		}
	}
	return
}

// Close stops all running sessions.
func (s *Supervisor) Close() {
	s.mu.Lock()
	sessions := make(map[string]*managedSession, len(s.sessions))
	for k, v := range s.sessions {
		sessions[k] = v
	}
	s.mu.Unlock()

	for id := range sessions {
		_ = s.Stop(id, true)
	}
}

func (s *Supervisor) forwardEvents(sessionID string, provider Provider, handle SessionHandle, buf *EventBuffer) {
	events := provider.Events(handle)
	if events == nil {
		return
	}
	for e := range events {
		buf.Append(e)

		// Update session state on terminal events
		if e.Done {
			s.mu.Lock()
			if ms, ok := s.sessions[sessionID]; ok {
				if e.Type == EventTypeSessionFailed {
					ms.info.State = SessionStateFailed
					ms.info.Error = e.Error
				} else if e.Type == EventTypeSessionStopped {
					ms.info.State = SessionStateStopped
				}
				ms.info.StoppedAt = time.Now().UTC()
			}
			s.mu.Unlock()
		}
	}
}
