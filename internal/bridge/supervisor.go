package bridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

// SupervisorOption configures optional Supervisor behaviour.
type SupervisorOption func(*Supervisor)

// WithStore attaches a SessionStore so that session metadata is persisted on
// every terminal state transition and reloaded at startup via LoadHistory.
func WithStore(store SessionStore) SupervisorOption {
	return func(s *Supervisor) {
		s.store = store
	}
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

	store   SessionStore
	histMu  sync.RWMutex
	history map[string]SessionInfo
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

func NewSupervisor(registry *Registry, policy Policy, outputBufSize int, idleTimeout time.Duration, opts ...SupervisorOption) *Supervisor {
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
		history:         make(map[string]SessionInfo),
	}
	for _, opt := range opts {
		opt(s)
	}
	go s.cleanupLoop()
	return s
}

// LoadHistory reads all persisted sessions from the store and places them in
// the in-memory history map so they are visible via Get and List. Sessions
// that were not in a terminal state (i.e. the daemon crashed mid-flight) are
// marked as SessionStateFailed with an "orphaned by daemon restart" message
// and their updated state is written back to the store.
//
// Call LoadHistory once, before serving requests.
func (s *Supervisor) LoadHistory() error {
	if s.store == nil {
		return nil
	}
	infos, err := s.store.LoadAll()
	if err != nil {
		return err
	}
	s.histMu.Lock()
	defer s.histMu.Unlock()
	for _, info := range infos {
		if info.State != SessionStateStopped && info.State != SessionStateFailed {
			info.State = SessionStateFailed
			if info.Error == "" {
				info.Error = "orphaned by daemon restart"
			}
			if info.StoppedAt.IsZero() {
				info.StoppedAt = nowUTC()
			}
			// Best-effort: ignore write errors during startup.
			if saveErr := s.store.Save(info); saveErr != nil {
				slog.Warn("session store: failed to update orphaned session", "session_id", info.SessionID, "error", saveErr)
			}
		}
		s.history[info.SessionID] = info
	}
	return nil
}

// persistSession writes info to the store if one is configured. Errors are
// logged at warn level and do not propagate — persistence is best-effort.
func (s *Supervisor) persistSession(info SessionInfo) {
	if s.store == nil {
		return
	}
	if err := s.store.Save(info); err != nil {
		slog.Warn("session store: failed to persist session", "session_id", info.SessionID, "error", err)
	}
}

// persistChunk writes a single PTY output chunk to the store. Errors are
// logged at warn level and do not propagate — persistence is best-effort.
func (s *Supervisor) persistChunk(sessionID string, chunk OutputChunk) {
	if s.store == nil {
		return
	}
	if err := s.store.SaveChunk(sessionID, chunk); err != nil {
		slog.Warn("session store: failed to persist chunk", "session_id", sessionID, "seq", chunk.Seq, "error", err)
	}
}

// attachHistory serves a read-only replay for a session that exists only in
// the persisted history (i.e. from a previous daemon lifetime). Returns
// ErrSessionNotFound if the session is not in history or has no store.
func (s *Supervisor) attachHistory(sessionID, clientID string, afterSeq uint64) (*AttachState, error) {
	if s.store == nil {
		return nil, ErrSessionNotFound
	}
	s.histMu.RLock()
	info, ok := s.history[sessionID]
	s.histMu.RUnlock()
	if !ok {
		return nil, ErrSessionNotFound
	}
	chunks, err := s.store.LoadChunks(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load chunks for %q: %w", sessionID, err)
	}
	var replay []OutputChunk
	for _, c := range chunks {
		if c.Seq > afterSeq {
			replay = append(replay, c)
		}
	}
	var oldest, last uint64
	if len(chunks) > 0 {
		oldest = chunks[0].Seq
		last = chunks[len(chunks)-1].Seq
	}
	// A closed channel signals EOF immediately to the server's streaming loop.
	closed := make(chan OutputChunk)
	close(closed)
	return &AttachState{
		ClientID:     clientID,
		Replay:       replay,
		Live:         closed,
		ReplayGap:    oldest > 0 && afterSeq > 0 && afterSeq < oldest-1,
		OldestSeq:    oldest,
		LastSeq:      last,
		ExitRecorded: info.ExitRecorded,
		ExitCode:     info.ExitCode,
		Cols:         info.Cols,
		Rows:         info.Rows,
	}, nil
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
	s.persistSession(info)
	return &info, nil
}

func (s *Supervisor) readLoop(ms *managedSession) {
	buf := make([]byte, 8192)
	for {
		n, err := ms.ptmx.Read(buf)
		if n > 0 {
			chunk := ms.buf.Append(buf[:n])
			s.persistChunk(ms.info.SessionID, chunk)
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

	s.persistSession(ms.snapshotInfo())
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
		// For stopped/failed sessions that were persisted in a previous daemon
		// lifetime, serve the stored chunks in read-only mode (no live channel).
		if state, err := s.attachHistory(sessionID, clientID, afterSeq); state != nil || err != ErrSessionNotFound {
			return state, err
		}
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
		// History sessions are served read-only; detach is a no-op.
		s.histMu.RLock()
		_, inHistory := s.history[sessionID]
		s.histMu.RUnlock()
		if inHistory {
			return nil
		}
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
	if ok {
		info := ms.snapshotInfo()
		return &info, nil
	}
	// Fall back to history (sessions persisted from a previous daemon lifetime).
	s.histMu.RLock()
	info, ok := s.history[sessionID]
	s.histMu.RUnlock()
	if ok {
		return &info, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
}

func (s *Supervisor) List(projectID string) []SessionInfo {
	// Snapshot live session IDs and their info under the live lock.
	s.mu.RLock()
	liveIDs := make(map[string]struct{}, len(s.sessions))
	out := make([]SessionInfo, 0, len(s.sessions))
	for id, ms := range s.sessions {
		liveIDs[id] = struct{}{}
		info := ms.snapshotInfo()
		if projectID != "" && info.ProjectID != projectID {
			continue
		}
		out = append(out, info)
	}
	s.mu.RUnlock()

	// Append historical sessions not present in the live map.
	s.histMu.RLock()
	for id, info := range s.history {
		if _, live := liveIDs[id]; live {
			continue
		}
		if projectID != "" && info.ProjectID != projectID {
			continue
		}
		out = append(out, info)
	}
	s.histMu.RUnlock()
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
