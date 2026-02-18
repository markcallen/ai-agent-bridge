package provider

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/markcallen/ai-agent-bridge/internal/bridge"
)

// StdioConfig configures a stdio-based provider adapter.
type StdioConfig struct {
	ProviderID     string
	Binary         string
	DefaultArgs    []string
	StartupTimeout time.Duration
	StopGrace      time.Duration
}

// StdioProvider manages agent sessions via subprocess stdio.
type StdioProvider struct {
	cfg     StdioConfig
	starter func(*exec.Cmd) error
}

var defaultCommandStarter = func(cmd *exec.Cmd) error { return cmd.Start() }

// NewStdioProvider creates a new stdio-based provider.
func NewStdioProvider(cfg StdioConfig) *StdioProvider {
	if cfg.StartupTimeout == 0 {
		cfg.StartupTimeout = 30 * time.Second
	}
	if cfg.StopGrace == 0 {
		cfg.StopGrace = 10 * time.Second
	}
	return &StdioProvider{
		cfg:     cfg,
		starter: defaultCommandStarter,
	}
}

func (p *StdioProvider) ID() string { return p.cfg.ProviderID }

func (p *StdioProvider) Health(ctx context.Context) error {
	path, err := resolveBinaryPath(p.cfg.Binary)
	if err != nil {
		return fmt.Errorf("binary %q not found: %w", p.cfg.Binary, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %q: %w", path, err)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("binary %q is not executable", path)
	}
	return nil
}

func (p *StdioProvider) Start(ctx context.Context, cfg bridge.SessionConfig) (bridge.SessionHandle, error) {
	binPath, err := resolveBinaryPath(p.cfg.Binary)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve binary %q: %v", bridge.ErrProviderUnavailable, p.cfg.Binary, err)
	}

	args := append([]string(nil), p.cfg.DefaultArgs...)
	// Merge any provider-specific options as additional args
	for k, v := range cfg.Options {
		if strings.HasPrefix(k, "arg:") {
			args = append(args, v)
		}
	}

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = cfg.RepoPath
	// Inherit minimal environment
	cmd.Env = filterEnv(os.Environ())

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	startErr := make(chan error, 1)
	starter := p.starter
	if starter == nil {
		starter = defaultCommandStarter
	}
	go func() {
		startErr <- starter(cmd)
	}()

	select {
	case err := <-startErr:
		if err != nil {
			return nil, fmt.Errorf("%w: start %s: %v", bridge.ErrProviderUnavailable, p.cfg.Binary, err)
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(p.cfg.StartupTimeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil, fmt.Errorf("%w: startup timeout after %s", bridge.ErrProviderUnavailable, p.cfg.StartupTimeout)
	}

	h := &stdioHandle{
		id:        cfg.SessionID,
		pid:       cmd.Process.Pid,
		cmd:       cmd,
		stdin:     stdin,
		events:    make(chan bridge.Event, 256),
		provider:  p.cfg.ProviderID,
		projectID: cfg.ProjectID,
		sessionID: cfg.SessionID,
		stopGrace: p.cfg.StopGrace,
		waitDone:  make(chan struct{}),
	}

	// Emit started event before launching goroutines to avoid race with channel close
	h.emit(bridge.Event{
		Type:   bridge.EventTypeSessionStarted,
		Stream: "system",
		Text:   "session started",
	})

	// Start output readers and exit watcher
	go h.readStream("stdout", stdout)
	go h.readStream("stderr", stderr)
	go h.waitForExit()

	return h, nil
}

func resolveBinaryPath(binary string) (string, error) {
	if strings.Contains(binary, "/") {
		if filepath.IsAbs(binary) {
			return binary, nil
		}
		return filepath.Abs(binary)
	}
	return exec.LookPath(binary)
}

func (p *StdioProvider) Send(handle bridge.SessionHandle, text string) error {
	h, ok := handle.(*stdioHandle)
	if !ok {
		return fmt.Errorf("invalid handle type")
	}
	return h.send(text)
}

func (p *StdioProvider) Stop(handle bridge.SessionHandle) error {
	h, ok := handle.(*stdioHandle)
	if !ok {
		return fmt.Errorf("invalid handle type")
	}
	return h.stop()
}

func (p *StdioProvider) Events(handle bridge.SessionHandle) <-chan bridge.Event {
	h, ok := handle.(*stdioHandle)
	if !ok {
		return nil
	}
	return h.events
}

// stdioHandle represents a running subprocess session.
type stdioHandle struct {
	id        string
	pid       int
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	events    chan bridge.Event
	provider  string
	projectID string
	sessionID string
	stopGrace time.Duration

	mu        sync.Mutex
	stopped   bool
	closed    bool
	closeOnce sync.Once
	waitDone  chan struct{} // closed when cmd.Wait() completes
	waitErr   error
}

func (h *stdioHandle) ID() string { return h.id }
func (h *stdioHandle) PID() int   { return h.pid }

func (h *stdioHandle) send(text string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return fmt.Errorf("session is stopped")
	}
	line := strings.TrimSpace(text)
	if line == "" {
		return fmt.Errorf("empty input")
	}
	_, err := io.WriteString(h.stdin, line+"\n")
	return err
}

func (h *stdioHandle) stop() error {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		// Wait for waitForExit goroutine to finish closing the channel
		<-h.waitDone
		return nil
	}
	h.stopped = true
	h.mu.Unlock()

	_ = h.stdin.Close()

	// Try graceful SIGTERM
	if h.cmd.Process != nil {
		_ = h.cmd.Process.Signal(syscall.SIGTERM)
	}

	// Wait for the waitForExit goroutine (which owns cmd.Wait) to finish
	select {
	case <-h.waitDone:
	case <-time.After(h.stopGrace):
		// Force kill if graceful shutdown timed out
		if h.cmd.Process != nil {
			_ = h.cmd.Process.Kill()
		}
		<-h.waitDone
	}

	return nil
}

func (h *stdioHandle) readStream(stream string, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // up to 1MB lines
	evType := bridge.EventTypeStdout
	if stream == "stderr" {
		evType = bridge.EventTypeStderr
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		h.emit(bridge.Event{
			Type:   evType,
			Stream: stream,
			Text:   line,
		})
	}
}

func (h *stdioHandle) waitForExit() {
	defer close(h.waitDone)

	err := h.cmd.Wait()

	h.mu.Lock()
	wasStopped := h.stopped
	h.stopped = true
	h.mu.Unlock()

	if wasStopped {
		// stop() was called; emit the stopped event
		h.emit(bridge.Event{
			Type:   bridge.EventTypeSessionStopped,
			Stream: "system",
			Text:   "session stopped",
			Done:   true,
		})
	} else if err != nil {
		h.emit(bridge.Event{
			Type:   bridge.EventTypeSessionFailed,
			Stream: "system",
			Text:   "agent process exited",
			Error:  err.Error(),
			Done:   true,
		})
	} else {
		h.emit(bridge.Event{
			Type:   bridge.EventTypeSessionStopped,
			Stream: "system",
			Text:   "agent process exited normally",
			Done:   true,
		})
	}

	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closed = true
		h.mu.Unlock()
		close(h.events)
	})
}

func (h *stdioHandle) emit(e bridge.Event) {
	e.Timestamp = time.Now().UTC()
	e.SessionID = h.sessionID
	e.ProjectID = h.projectID
	e.Provider = h.provider

	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if closed {
		return
	}

	select {
	case h.events <- e:
	default:
		// Channel full, drop event
	}
}

// filterEnv returns a filtered environment excluding sensitive variables.
func filterEnv(env []string) []string {
	blocked := map[string]bool{
		"AWS_SECRET_ACCESS_KEY": true,
		"AWS_SESSION_TOKEN":     true,
		"SLACK_BOT_TOKEN":       true,
		"SLACK_SIGNING_SECRET":  true,
		"DISCORD_TOKEN":         true,
	}
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		key, _, ok := strings.Cut(e, "=")
		if ok && blocked[key] {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}
