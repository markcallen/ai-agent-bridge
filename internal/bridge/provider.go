package bridge

import (
	"context"
	"os/exec"
	"regexp"
	"time"
)

// Provider describes an interactive PTY-backed CLI provider.
type Provider interface {
	ID() string
	Binary() string
	PromptPattern() *regexp.Regexp
	StartupTimeout() time.Duration
	StopGrace() time.Duration
	BuildCommand(ctx context.Context, cfg SessionConfig) (*exec.Cmd, error)
	ValidateStartup(ctx context.Context) error
	Health(ctx context.Context) error
	Version(ctx context.Context) (string, error)
}

// SessionConfig holds configuration for starting a new provider session.
type SessionConfig struct {
	ProjectID   string
	SessionID   string
	RepoPath    string
	Options     map[string]string
	InitialCols uint32
	InitialRows uint32
}

// SessionState represents the lifecycle state of a session.
type SessionState int

const (
	SessionStateStarting SessionState = iota + 1
	SessionStateRunning
	SessionStateAttached
	SessionStateStopping
	SessionStateStopped
	SessionStateFailed
)

// SessionInfo holds metadata about a running session.
type SessionInfo struct {
	SessionID        string
	ProjectID        string
	Provider         string
	State            SessionState
	CreatedAt        time.Time
	StoppedAt        time.Time
	Error            string
	Attached         bool
	AttachedClientID string
	ExitRecorded     bool
	ExitCode         int
	OldestSeq        uint64
	LastSeq          uint64
	Cols             uint32
	Rows             uint32
}

// OutputChunk is one retained PTY byte chunk.
type OutputChunk struct {
	Seq       uint64
	Timestamp time.Time
	Payload   []byte
}
