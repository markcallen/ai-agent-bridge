package bridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type AttachState struct {
	ClientID     string
	Replay       []OutputChunk
	Live         <-chan OutputChunk
	ReplayGap    bool
	OldestSeq    uint64
	LastSeq      uint64
	ExitRecorded bool
	ExitCode     int
	Cols         uint32
	Rows         uint32
}

// Supervisor manages the lifecycle of PTY-backed provider sessions.
type Supervisor struct {
	registry        *Registry
	policy          Policy
	bufSize         int
	idleTimeout     time.Duration
	cleanupInterval time.Duration

	mu       sync.RWMutex
	sessions map[string]*managedSession
	done     chan struct{}
}

type managedSession struct {
	mu           sync.Mutex
	info         SessionInfo
	provider     Provider
	cmd          *exec.Cmd
	ptmx         *os.File
	buf          *ByteBuffer
	cancel       context.CancelFunc
	stopGrace    time.Duration
	lastActivity time.Time
	forceStop    bool

	attachedClient string
	live           chan OutputChunk
}

func NewSupervisor(registry *Registry, policy Policy, outputBufSize int, idleTimeout time.Duration) *Supervisor {
	if outputBufSize <= 0 {
		outputBufSize = 8 << 20
	}
	s := &Supervisor{
		registry:        registry,
		policy:          policy,
		bufSize:         outputBufSize,
		idleTimeout:     idleTimeout,
		cleanupInterval: 30 * time.Second,
		sessions:        make(map[string]*managedSession),
		done:            make(chan struct{}),
	}
	go s.cleanupLoop()
	return s
}

func (s *Supervisor) cleanupLoop() {
	ticker := time.NewTicker(s.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			if s.idleTimeout <= 0 {
				continue
			}
			var idle []string
			s.mu.RLock()
			for id, ms := range s.sessions {
				ms.mu.Lock()
				if (ms.info.State == SessionStateRunning || ms.info.State == SessionStateAttached) &&
					time.Since(ms.lastActivity) > s.idleTimeout {
					idle = append(idle, id)
				}
				ms.mu.Unlock()
			}
			s.mu.RUnlock()
			for _, id := range idle {
				_ = s.Stop(id, false)
			}
		}
	}
}

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
	if err := s.policy.ValidateRepoPath(cfg.RepoPath); err != nil {
		return nil, err
	}

	s.mu.Lock()
	if _, exists := s.sessions[cfg.SessionID]; exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrSessionAlreadyExists, cfg.SessionID)
	}
	projectCount := 0
	globalCount := 0
	for _, ms := range s.sessions {
		if ms.info.State == SessionStateRunning || ms.info.State == SessionStateStarting || ms.info.State == SessionStateAttached {
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

	provider, err := s.registry.Get(cfg.Options["provider"])
	if err != nil {
		return nil, err
	}
	if err := provider.Health(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	if cfg.InitialCols == 0 {
		cfg.InitialCols = 120
	}
	if cfg.InitialRows == 0 {
		cfg.InitialRows = 40
	}

	sessionCtx, cancel := context.WithCancel(context.Background())
	cmd, err := provider.BuildCommand(sessionCtx, cfg)
	if err != nil {
		cancel()
		return nil, err
	}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cfg.InitialCols),
		Rows: uint16(cfg.InitialRows),
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start pty session: %w", err)
	}

	now := nowUTC()
	ms := &managedSession{
		info: SessionInfo{
			SessionID: cfg.SessionID,
			ProjectID: cfg.ProjectID,
			Provider:  provider.ID(),
			State:     SessionStateRunning,
			CreatedAt: now,
			Cols:      cfg.InitialCols,
			Rows:      cfg.InitialRows,
		},
		provider:     provider,
		cmd:          cmd,
		ptmx:         ptmx,
		buf:          NewByteBuffer(s.bufSize),
		cancel:       cancel,
		stopGrace:    provider.StopGrace(),
		lastActivity: time.Now(),
	}

	s.mu.Lock()
	if _, exists := s.sessions[cfg.SessionID]; exists {
		s.mu.Unlock()
		cancel()
		_ = ptmx.Close()
		return nil, fmt.Errorf("%w: %q", ErrSessionAlreadyExists, cfg.SessionID)
	}
	s.sessions[cfg.SessionID] = ms
	s.mu.Unlock()

	go s.readLoop(ms)
	go s.waitLoop(ms)

	info := ms.snapshotInfo()
	return &info, nil
}

func (s *Supervisor) readLoop(ms *managedSession) {
	buf := make([]byte, 8192)
	for {
		n, err := ms.ptmx.Read(buf)
		if n > 0 {
			chunk := ms.buf.Append(buf[:n])
			ms.mu.Lock()
			ms.info.OldestSeq = ms.buf.OldestSeq()
			ms.info.LastSeq = ms.buf.LastSeq()
			ms.lastActivity = time.Now()
			live := ms.live
			ms.mu.Unlock()
			if live != nil {
				live <- chunk
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				ms.mu.Lock()
				if ms.info.Error == "" && !ms.info.ExitRecorded {
					ms.info.Error = err.Error()
				}
				ms.mu.Unlock()
			}
			return
		}
	}
}

func (s *Supervisor) waitLoop(ms *managedSession) {
	err := ms.cmd.Wait()

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	ms.mu.Lock()
	ms.info.StoppedAt = nowUTC()
	ms.info.ExitRecorded = true
	ms.info.ExitCode = exitCode
	if err != nil && !ms.forceStop {
		ms.info.State = SessionStateFailed
		if ms.info.Error == "" {
			ms.info.Error = err.Error()
		}
	} else {
		ms.info.State = SessionStateStopped
	}
	ms.cancel()
	ms.mu.Unlock()
}

func (s *Supervisor) Stop(sessionID string, force bool) error {
	s.mu.RLock()
	ms, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}

	ms.mu.Lock()
	if ms.info.State == SessionStateStopped || ms.info.State == SessionStateFailed {
		ms.mu.Unlock()
		return nil
	}
	ms.info.State = SessionStateStopping
	ms.forceStop = force
	pid := ms.cmd.Process.Pid
	grace := ms.stopGrace
	ms.mu.Unlock()

	if force {
		if pid > 0 {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
		return nil
	}
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
	}

	go func() {
		time.Sleep(grace)
		ms.mu.Lock()
		state := ms.info.State
		pid := ms.cmd.Process.Pid
		ms.mu.Unlock()
		if state == SessionStateStopping && pid > 0 {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	}()
	return nil
}

func (s *Supervisor) WriteInput(sessionID, clientID string, data []byte) (int, error) {
	if err := s.policy.ValidateInputBytes(data); err != nil {
		return 0, err
	}
	s.mu.RLock()
	ms, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	ms.mu.Lock()
	if ms.attachedClient == "" {
		ms.mu.Unlock()
		return 0, ErrClientNotAttached
	}
	if ms.attachedClient != clientID {
		ms.mu.Unlock()
		return 0, ErrClientMismatch
	}
	ms.lastActivity = time.Now()
	ms.mu.Unlock()
	n, err := ms.ptmx.Write(data)
	if err != nil {
		return n, err
	}
	return n, nil
}

func (s *Supervisor) Resize(sessionID, clientID string, cols, rows uint32) error {
	s.mu.RLock()
	ms, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	ms.mu.Lock()
	if ms.attachedClient == "" {
		ms.mu.Unlock()
		return ErrClientNotAttached
	}
	if ms.attachedClient != clientID {
		ms.mu.Unlock()
		return ErrClientMismatch
	}
	ms.info.Cols = cols
	ms.info.Rows = rows
	ms.lastActivity = time.Now()
	ms.mu.Unlock()
	return pty.Setsize(ms.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (s *Supervisor) Attach(sessionID, clientID string, afterSeq uint64) (*AttachState, error) {
	s.mu.RLock()
	ms, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.attachedClient != "" {
		return nil, ErrSessionAlreadyAttached
	}
	ms.attachedClient = clientID
	ms.live = make(chan OutputChunk, 128)
	ms.info.Attached = true
	ms.info.AttachedClientID = clientID
	ms.info.State = SessionStateAttached
	ms.lastActivity = time.Now()

	oldest := ms.buf.OldestSeq()
	last := ms.buf.LastSeq()
	return &AttachState{
		ClientID:     clientID,
		Replay:       ms.buf.After(afterSeq),
		Live:         ms.live,
		ReplayGap:    oldest > 0 && afterSeq > 0 && afterSeq < oldest-1,
		OldestSeq:    oldest,
		LastSeq:      last,
		ExitRecorded: ms.info.ExitRecorded,
		ExitCode:     ms.info.ExitCode,
		Cols:         ms.info.Cols,
		Rows:         ms.info.Rows,
	}, nil
}

func (s *Supervisor) Detach(sessionID, clientID string) error {
	s.mu.RLock()
	ms, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.attachedClient != clientID {
		return ErrClientMismatch
	}
	ms.attachedClient = ""
	ms.live = nil
	ms.info.Attached = false
	ms.info.AttachedClientID = ""
	if ms.info.State == SessionStateAttached {
		ms.info.State = SessionStateRunning
	}
	return nil
}

func (s *Supervisor) Get(sessionID string) (*SessionInfo, error) {
	s.mu.RLock()
	ms, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	info := ms.snapshotInfo()
	return &info, nil
}

func (s *Supervisor) List(projectID string) []SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SessionInfo, 0, len(s.sessions))
	for _, ms := range s.sessions {
		info := ms.snapshotInfo()
		if projectID != "" && info.ProjectID != projectID {
			continue
		}
		out = append(out, info)
	}
	return out
}

func (s *Supervisor) Close() {
	close(s.done)
	s.mu.RLock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	for _, id := range ids {
		_ = s.Stop(id, true)
	}
}

func (ms *managedSession) snapshotInfo() SessionInfo {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	info := ms.info
	info.OldestSeq = ms.buf.OldestSeq()
	info.LastSeq = ms.buf.LastSeq()
	return info
}
