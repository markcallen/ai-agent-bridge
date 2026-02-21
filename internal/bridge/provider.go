package bridge

import (
	"context"
	"time"
)

// Provider defines the interface that all AI agent adapters must implement.
type Provider interface {
	// ID returns the provider identifier (e.g. "codex", "claude", "opencode").
	ID() string

	// Start spawns a new agent session and returns a handle.
	Start(ctx context.Context, cfg SessionConfig) (SessionHandle, error)

	// Send writes input text to the agent's stdin.
	Send(handle SessionHandle, text string) error

	// Stop terminates the agent session.
	Stop(handle SessionHandle) error

	// Events returns a channel that emits events from the agent.
	Events(handle SessionHandle) <-chan Event

	// Health checks if the provider binary is available.
	Health(ctx context.Context) error
}

// SessionConfig holds configuration for starting a new agent session.
type SessionConfig struct {
	ProjectID string
	SessionID string
	RepoPath  string
	Options   map[string]string
}

// SessionHandle represents a running agent session.
type SessionHandle interface {
	ID() string
	PID() int
}

// Event represents a single event emitted by an agent session.
type Event struct {
	Timestamp time.Time
	SessionID string
	ProjectID string
	Provider  string
	Type      EventType
	Stream    string // "system", "stdout", "stderr"
	Text      string
	Done      bool
	Error     string
}

// EventType enumerates the types of events an agent can emit.
type EventType int

const (
	EventTypeSessionStarted EventType = iota + 1
	EventTypeSessionStopped
	EventTypeSessionFailed
	EventTypeStdout
	EventTypeStderr
	EventTypeInputReceived
	EventTypeBufferOverflow
	// EventTypeAgentReady signals that the agent process is ready for input.
	EventTypeAgentReady
	// EventTypeResponseComplete signals that the agent has finished responding.
	EventTypeResponseComplete
)
