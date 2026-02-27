package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/markcallen/ai-agent-bridge/internal/bridge"
)

// StdioConfig configures a stdio-based provider adapter.
type StdioConfig struct {
	ProviderID     string
	Binary         string
	DefaultArgs    []string
	StartupTimeout time.Duration
	StopGrace      time.Duration
	UsePTY         bool
	StreamJSON     bool
	// PromptPattern is a regex matched against each output line for PTY-based
	// providers. The first match emits AGENT_READY; subsequent matches after
	// output has been seen emit RESPONSE_COMPLETE.
	PromptPattern string
}

// stream-json parse structs for Claude Code CLI's --output-format stream-json.
// Claude Code emits NDJSON where each line is one of these events.
// We extract text from "assistant" events and use "system"/"result" for signals.
type claudeStreamEvent struct {
	Type    string         `json:"type"`    // "system", "user", "assistant", "result"
	Subtype string         `json:"subtype"` // "init" for system events, "success"/"error" for result
	Message *claudeMessage `json:"message"` // present when type == "assistant"
}

type claudeMessage struct {
	Content []claudeContent `json:"content"`
}

type claudeContent struct {
	Type string `json:"type"` // "text", "tool_use", etc.
	Text string `json:"text"` // non-empty when type == "text"
}

// StdioProvider manages agent sessions via subprocess stdio.
type StdioProvider struct {
	cfg      StdioConfig
	promptRe *regexp.Regexp
	starter  func(*exec.Cmd) error
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
	p := &StdioProvider{
		cfg:     cfg,
		starter: defaultCommandStarter,
	}
	if cfg.PromptPattern != "" {
		p.promptRe = regexp.MustCompile(cfg.PromptPattern)
	}
	return p
}

func (p *StdioProvider) ID() string { return p.cfg.ProviderID }

func (p *StdioProvider) Version(ctx context.Context) (string, error) {
	path, err := resolveBinaryPath(p.cfg.Binary)
	if err != nil {
		return "", fmt.Errorf("binary %q not found: %w", p.cfg.Binary, err)
	}
	cmd := exec.CommandContext(ctx, path, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("version check: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

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

	if p.cfg.UsePTY {
		return p.startPTY(ctx, cfg, binPath, args)
	}

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = cfg.RepoPath
	// Inherit minimal environment
	cmd.Env = filterEnv(os.Environ())
	// Run in its own process group so SIGTERM/SIGKILL sent by the agent's
	// process tree cannot propagate back to the bridge process.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

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
		id:         cfg.SessionID,
		pid:        cmd.Process.Pid,
		cmd:        cmd,
		stdin:      stdin,
		events:     make(chan bridge.Event, 256),
		provider:   p.cfg.ProviderID,
		projectID:  cfg.ProjectID,
		sessionID:  cfg.SessionID,
		stopGrace:  p.cfg.StopGrace,
		streamJSON: p.cfg.StreamJSON,
		promptRe:   p.promptRe,
		waitDone:   make(chan struct{}),
	}

	// Emit started event before launching goroutines to avoid race with channel close
	h.emit(bridge.Event{
		Type:   bridge.EventTypeSessionStarted,
		Stream: "system",
		Text:   "session started",
	})

	// For stream-json providers, the process reads from stdin and is immediately
	// ready; emit AGENT_READY now rather than waiting for a prompt pattern.
	if p.cfg.StreamJSON {
		h.emit(bridge.Event{
			Type:   bridge.EventTypeAgentReady,
			Stream: "system",
			Text:   "agent ready",
		})
	}

	// Start output readers and exit watcher.
	h.streamWG.Add(2)
	go h.readStream("stdout", stdout)
	go h.readStream("stderr", stderr)
	go h.waitForExit()

	return h, nil
}

func (p *StdioProvider) startPTY(ctx context.Context, cfg bridge.SessionConfig, binPath string, args []string) (bridge.SessionHandle, error) {
	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = cfg.RepoPath
	cmd.Env = filterEnv(os.Environ())

	type ptyStart struct {
		file *os.File
		err  error
	}

	startCh := make(chan ptyStart, 1)
	go func() {
		f, err := pty.Start(cmd)
		startCh <- ptyStart{file: f, err: err}
	}()

	var ptmx *os.File
	select {
	case res := <-startCh:
		if res.err != nil {
			return nil, fmt.Errorf("%w: start %s: %v", bridge.ErrProviderUnavailable, p.cfg.Binary, res.err)
		}
		ptmx = res.file
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(p.cfg.StartupTimeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil, fmt.Errorf("%w: startup timeout after %s", bridge.ErrProviderUnavailable, p.cfg.StartupTimeout)
	}

	h := &stdioHandle{
		id:         cfg.SessionID,
		pid:        cmd.Process.Pid,
		cmd:        cmd,
		stdin:      ptmx,
		events:     make(chan bridge.Event, 256),
		provider:   p.cfg.ProviderID,
		projectID:  cfg.ProjectID,
		sessionID:  cfg.SessionID,
		stopGrace:  p.cfg.StopGrace,
		streamJSON: p.cfg.StreamJSON,
		promptRe:   p.promptRe,
		waitDone:   make(chan struct{}),
	}

	h.emit(bridge.Event{
		Type:   bridge.EventTypeSessionStarted,
		Stream: "system",
		Text:   "session started",
	})

	h.streamWG.Add(1)
	go h.readStream("stdout", ptmx)
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
	id         string
	pid        int
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	events     chan bridge.Event
	provider   string
	projectID  string
	sessionID  string
	stopGrace  time.Duration
	streamJSON bool
	promptRe   *regexp.Regexp // non-nil for PTY providers with a prompt pattern

	mu        sync.Mutex
	stopped   bool
	closed    bool
	closeOnce sync.Once
	waitDone  chan struct{} // closed when cmd.Wait() completes
	waitErr   error
	streamWG  sync.WaitGroup
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
	if h.streamJSON {
		msg := struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			SessionID       string  `json:"session_id"`
			ParentToolUseID *string `json:"parent_tool_use_id"`
		}{
			Type:            "user",
			SessionID:       "default",
			ParentToolUseID: nil,
		}
		msg.Message.Role = "user"
		msg.Message.Content = line
		data, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("marshal stream-json input: %w", err)
		}
		_, err = h.stdin.Write(append(data, '\n'))
		return err
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

	// SIGTERM the process group so child processes (e.g. Node.js workers
	// spawned by Claude Code) are also signalled. When Setpgid was set, the
	// child's PID equals its PGID. Fall back to the individual process signal
	// if the PID is not yet available.
	if h.pid > 0 {
		_ = syscall.Kill(-h.pid, syscall.SIGTERM)
	} else if h.cmd.Process != nil {
		_ = h.cmd.Process.Signal(syscall.SIGTERM)
	}

	// Wait for the waitForExit goroutine (which owns cmd.Wait) to finish
	select {
	case <-h.waitDone:
	case <-time.After(h.stopGrace):
		// Force kill if graceful shutdown timed out
		if h.pid > 0 {
			_ = syscall.Kill(-h.pid, syscall.SIGKILL)
		} else if h.cmd.Process != nil {
			_ = h.cmd.Process.Kill()
		}
		<-h.waitDone
	}

	return nil
}

func (h *stdioHandle) readStream(stream string, r io.Reader) {
	defer h.streamWG.Done()

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // up to 1MB lines
	evType := bridge.EventTypeStdout
	if stream == "stderr" {
		evType = bridge.EventTypeStderr
	}

	// State for PTY prompt detection.
	promptReady := false // true after AGENT_READY has been emitted
	sawOutput := false   // true after non-prompt output since last prompt match

	for sc.Scan() {
		// PTY output has \r\n line endings; strip the carriage return.
		line := strings.TrimRight(sc.Text(), "\r")

		if strings.TrimSpace(line) == "" {
			continue
		}

		// --- stream-json mode (Claude Code SDK output) ---
		if h.streamJSON && stream == "stdout" {
			var ev claudeStreamEvent
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				continue // skip unparseable lines
			}
			switch ev.Type {
			case "assistant":
				// Extract text content and stream it.
				if ev.Message != nil {
					for _, c := range ev.Message.Content {
						if c.Type == "text" && c.Text != "" {
							h.emit(bridge.Event{
								Type:   evType,
								Stream: stream,
								Text:   c.Text,
							})
						}
					}
				}
			case "result":
				// Claude has finished responding to the last input.
				h.emit(bridge.Event{
					Type:   bridge.EventTypeResponseComplete,
					Stream: "system",
					Text:   "response complete",
				})
			}
			continue
		}

		// --- PTY prompt-pattern mode ---
		if h.promptRe != nil && stream == "stdout" && h.promptRe.MatchString(line) {
			if !promptReady {
				// First prompt appearance: agent has initialised.
				promptReady = true
				h.emit(bridge.Event{
					Type:   bridge.EventTypeAgentReady,
					Stream: "system",
					Text:   "agent ready",
				})
			} else if sawOutput {
				// Prompt returned after output: response is complete.
				sawOutput = false
				h.emit(bridge.Event{
					Type:   bridge.EventTypeResponseComplete,
					Stream: "system",
					Text:   "response complete",
				})
			}
			// Do not emit the raw prompt line as stdout.
			continue
		}

		// --- regular output ---
		if h.promptRe != nil && stream == "stdout" {
			sawOutput = true
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

	// Drain all stdout/stderr output BEFORE calling cmd.Wait(). Go's
	// StdoutPipe/StderrPipe add the read ends to closeAfterWait, so cmd.Wait()
	// closes those file descriptors. If we called Wait() first, the scanner in
	// readStream would get a bad-fd error mid-stream and drop buffered output.
	// The process exiting closes the write end of the pipes, so readStream gets
	// a natural EOF that lets it drain everything before returning.
	h.streamWG.Wait()

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

// filterEnv returns a filtered environment excluding sensitive variables and
// variables that interfere with subprocess behaviour.
func filterEnv(env []string) []string {
	blocked := map[string]bool{
		// Credentials that child processes should not inherit
		"AWS_SECRET_ACCESS_KEY": true,
		"AWS_SESSION_TOKEN":     true,
		"SLACK_BOT_TOKEN":       true,
		"SLACK_SIGNING_SECRET":  true,
		"DISCORD_TOKEN":         true,
		// Claude Code sets this to prevent nested invocations; unset it so
		// the bridge can launch claude as a managed subprocess.
		"CLAUDECODE": true,
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
