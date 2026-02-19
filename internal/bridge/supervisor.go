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
	registry  *Registry
	policy    Policy
	bufSize   int
	subConfig SubscriberConfig
	redact    func(string) string

	mu       sync.RWMutex
	sessions map[string]*managedSession // keyed by session_id

	done chan struct{} // closed by Close to stop background goroutines
}

type managedSession struct {
	info   SessionInfo
	handle SessionHandle
	buf    *EventBuffer
	subMgr *SubscriberManager
	cancel context.CancelFunc
}

// NewSupervisor creates a new session supervisor.
func NewSupervisor(registry *Registry, policy Policy, eventBufSize int, subConfig SubscriberConfig) *Supervisor {
	if eventBufSize <= 0 {
		eventBufSize = 10000
	}
	s := &Supervisor{
		registry:  registry,
		policy:    policy,
		bufSize:   eventBufSize,
		subConfig: subConfig,
		sessions:  make(map[string]*managedSession),
		done:      make(chan struct{}),
	}
	go s.cleanupLoop()
	return s
}

// SetRedactor configures a redaction function for buffered event text/error.
func (s *Supervisor) SetRedactor(fn func(string) string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redact = fn
}

func (s *Supervisor) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.mu.RLock()
			for _, ms := range s.sessions {
				ms.subMgr.CleanupExpired()
			}
			s.mu.RUnlock()
		}
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
	subMgr := NewSubscriberManager(buf, s.subConfig)
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
		subMgr: subMgr,
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
		Text:      s.redactString(text),
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

// SubscriberManager returns the subscriber manager for a session.
func (s *Supervisor) SubscriberManager(sessionID string) (*SubscriberManager, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ms, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	return ms.subMgr, nil
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

// Close stops all running sessions and background goroutines.
func (s *Supervisor) Close() {
	// Signal cleanup goroutine to stop.
	select {
	case <-s.done:
		// Already closed.
	default:
		close(s.done)
	}

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
		e.Text = s.redactString(e.Text)
		e.Error = s.redactString(e.Error)
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

func (s *Supervisor) redactString(text string) string {
	s.mu.RLock()
	fn := s.redact
	s.mu.RUnlock()
	if fn == nil {
		return text
	}
	return fn(text)
}
