package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/markcallen/ai-agent-bridge/internal/bridge"
)

// CodexExecConfig configures the CodexExecProvider.
type CodexExecConfig struct {
	ProviderID string
	Binary     string
	ExtraArgs  []string
	StopGrace  time.Duration
}

// CodexExecProvider implements bridge.Provider using "codex exec --json -".
// Each Send spawns a new one-shot subprocess. The first send captures the
// thread_id from "thread.started"; subsequent sends use
// "codex exec resume <thread-id> --json -" to continue the same thread.
type CodexExecProvider struct {
	providerID string
	binary     string
	extraArgs  []string
	stopGrace  time.Duration
}

// NewCodexExecProvider creates a new CodexExecProvider.
func NewCodexExecProvider(cfg CodexExecConfig) *CodexExecProvider {
	if cfg.Binary == "" {
		cfg.Binary = "codex"
	}
	if cfg.StopGrace == 0 {
		cfg.StopGrace = 10 * time.Second
	}
	return &CodexExecProvider{
		providerID: cfg.ProviderID,
		binary:     cfg.Binary,
		extraArgs:  cfg.ExtraArgs,
		stopGrace:  cfg.StopGrace,
	}
}

func (p *CodexExecProvider) ID() string { return p.providerID }

func (p *CodexExecProvider) Health(ctx context.Context) error {
	path, err := resolveBinaryPath(p.binary)
	if err != nil {
		return fmt.Errorf("binary %q not found: %w", p.binary, err)
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

func (p *CodexExecProvider) Start(ctx context.Context, cfg bridge.SessionConfig) (bridge.SessionHandle, error) {
	h := &codexExecHandle{
		id:         cfg.SessionID,
		providerID: p.providerID,
		projectID:  cfg.ProjectID,
		sessionID:  cfg.SessionID,
		repoPath:   cfg.RepoPath,
		binary:     p.binary,
		extraArgs:  p.extraArgs,
		stopGrace:  p.stopGrace,
		events:     make(chan bridge.Event, 256),
	}
	h.emit(bridge.Event{
		Type:   bridge.EventTypeSessionStarted,
		Stream: "system",
		Text:   "session started",
	})
	h.emit(bridge.Event{
		Type:   bridge.EventTypeAgentReady,
		Stream: "system",
		Text:   "agent ready",
	})
	return h, nil
}

func (p *CodexExecProvider) Send(handle bridge.SessionHandle, text string) error {
	h, ok := handle.(*codexExecHandle)
	if !ok {
		return fmt.Errorf("invalid handle type")
	}
	return h.send(text)
}

func (p *CodexExecProvider) Stop(handle bridge.SessionHandle) error {
	h, ok := handle.(*codexExecHandle)
	if !ok {
		return fmt.Errorf("invalid handle type")
	}
	h.stop()
	return nil
}

func (p *CodexExecProvider) Events(handle bridge.SessionHandle) <-chan bridge.Event {
	h, ok := handle.(*codexExecHandle)
	if !ok {
		return nil
	}
	return h.events
}

// codexExecHandle holds the state for a single codex exec session.
type codexExecHandle struct {
	id         string
	providerID string
	projectID  string
	sessionID  string
	repoPath   string
	binary     string
	extraArgs  []string
	stopGrace  time.Duration

	mu        sync.Mutex
	threadID  string              // set from "thread.started"; used for resume
	busy      bool                // true while a subprocess is running
	stopped   bool                // true after Stop() called
	closed    bool                // true after events channel closed
	cancelExec context.CancelFunc // cancels the in-flight exec subprocess

	events    chan bridge.Event
	closeOnce sync.Once
}

func (h *codexExecHandle) ID() string { return h.id }
func (h *codexExecHandle) PID() int   { return 0 }

func (h *codexExecHandle) send(text string) error {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return fmt.Errorf("session is stopped")
	}
	if h.busy {
		h.mu.Unlock()
		return fmt.Errorf("session is busy: previous prompt still in progress")
	}
	h.busy = true
	threadID := h.threadID
	h.mu.Unlock()

	go h.runExec(text, threadID)
	return nil
}

func (h *codexExecHandle) runExec(prompt, threadID string) {
	ctx, cancel := context.WithCancel(context.Background())
	h.mu.Lock()
	h.cancelExec = cancel
	h.mu.Unlock()
	defer cancel()

	// Build args: extra_args first (global flags like -c), then exec subcommand.
	// First turn:   codex [extra...] exec --json -
	// Resume turn:  codex [extra...] exec resume <thread-id> --json -
	args := append([]string(nil), h.extraArgs...)
	args = append(args, "exec")
	if threadID != "" {
		args = append(args, "resume", threadID)
	}
	args = append(args, "--json", "-")

	binPath, err := resolveBinaryPath(h.binary)
	if err != nil {
		h.emitExecError(fmt.Sprintf("resolve binary: %v", err), true)
		return
	}

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = h.repoPath
	cmd.Env = filterEnv(os.Environ())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		h.emitExecError(fmt.Sprintf("stdin pipe: %v", err), true)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		h.emitExecError(fmt.Sprintf("stdout pipe: %v", err), true)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		h.emitExecError(fmt.Sprintf("stderr pipe: %v", err), true)
		return
	}

	if err := cmd.Start(); err != nil {
		h.emitExecError(fmt.Sprintf("start exec: %v", err), true)
		return
	}

	// Write prompt to stdin then close so codex sees EOF.
	_, _ = io.WriteString(stdin, strings.TrimSpace(prompt)+"\n")
	_ = stdin.Close()

	// Drain stderr in background.
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			h.emit(bridge.Event{
				Type:   bridge.EventTypeStderr,
				Stream: "stderr",
				Text:   line,
			})
		}
	}()

	// Parse JSONL from stdout.
	newThreadID := ""
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev codexJSONEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "thread.started":
			newThreadID = ev.ThreadID
		case "item.completed":
			if ev.Item == nil {
				continue
			}
			switch ev.Item.Type {
			case "agent_message":
				if ev.Item.Text != "" {
					h.emit(bridge.Event{
						Type:   bridge.EventTypeStdout,
						Stream: "stdout",
						Text:   ev.Item.Text,
					})
				}
			case "command_execution":
				if ev.Item.AggregatedOutput != "" {
					h.emit(bridge.Event{
						Type:   bridge.EventTypeStdout,
						Stream: "stdout",
						Text:   ev.Item.AggregatedOutput,
					})
				}
			}
		}
	}

	waitErr := cmd.Wait()

	h.mu.Lock()
	if newThreadID != "" {
		h.threadID = newThreadID
	}
	h.busy = false
	wasStopped := h.stopped
	h.mu.Unlock()

	if wasStopped {
		h.closeSession()
		return
	}

	if waitErr != nil {
		h.emit(bridge.Event{
			Type:   bridge.EventTypeStderr,
			Stream: "stderr",
			Text:   fmt.Sprintf("codex exec exited: %v", waitErr),
		})
	}

	// Always emit RESPONSE_COMPLETE so the client can send the next prompt.
	h.emit(bridge.Event{
		Type:   bridge.EventTypeResponseComplete,
		Stream: "system",
		Text:   "response complete",
	})
}

// emitExecError emits an error event. If fatal is true the session is closed;
// otherwise a RESPONSE_COMPLETE is emitted to unblock the client.
func (h *codexExecHandle) emitExecError(msg string, fatal bool) {
	h.mu.Lock()
	h.busy = false
	h.mu.Unlock()

	if fatal {
		h.emit(bridge.Event{
			Type:   bridge.EventTypeSessionFailed,
			Stream: "system",
			Text:   "session failed",
			Error:  msg,
			Done:   true,
		})
		h.closeSession()
		return
	}
	h.emit(bridge.Event{
		Type:   bridge.EventTypeStderr,
		Stream: "stderr",
		Text:   msg,
	})
	h.emit(bridge.Event{
		Type:   bridge.EventTypeResponseComplete,
		Stream: "system",
		Text:   "response complete",
	})
}

func (h *codexExecHandle) stop() {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	h.stopped = true
	cancelExec := h.cancelExec
	busy := h.busy
	h.mu.Unlock()

	if cancelExec != nil {
		cancelExec()
	}
	// If not busy, runExec is not running; close the session directly.
	if !busy {
		h.closeSession()
	}
	// If busy, runExec will call closeSession when it finishes.
}

func (h *codexExecHandle) closeSession() {
	h.emit(bridge.Event{
		Type:   bridge.EventTypeSessionStopped,
		Stream: "system",
		Text:   "session stopped",
		Done:   true,
	})
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closed = true
		h.mu.Unlock()
		close(h.events)
	})
}

func (h *codexExecHandle) emit(e bridge.Event) {
	e.Timestamp = time.Now().UTC()
	e.SessionID = h.sessionID
	e.ProjectID = h.projectID
	e.Provider = h.providerID

	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if closed {
		return
	}

	select {
	case h.events <- e:
	default:
		// Channel full; drop event.
	}
}

// codexJSONEvent is a parsed line from "codex exec --json" JSONL output.
type codexJSONEvent struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Item     *codexJSONItem  `json:"item"`
}

type codexJSONItem struct {
	Type            string `json:"type"`
	Text            string `json:"text"`
	AggregatedOutput string `json:"aggregated_output"`
}
