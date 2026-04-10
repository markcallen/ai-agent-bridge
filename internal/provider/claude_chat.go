package provider

import "time"

// NewClaudeChatProvider creates a stream-JSON provider that runs
// `claude --output-format stream-json --verbose` without a PTY.
// stdout is newline-delimited JSON; the supervisor's readLoopStreamJSON
// parser extracts text and thinking deltas as typed OutputChunks.
func NewClaudeChatProvider() *StdioProvider {
	return NewStdioProvider(StdioConfig{
		ProviderID:     "claude-chat",
		Binary:         "claude",
		DefaultArgs:    []string{"--output-format", "stream-json", "--verbose"},
		StartupTimeout: 60 * time.Second,
		StopGrace:      10 * time.Second,
		StartupProbe:   "none",
		RequiredEnv:    []string{"ANTHROPIC_API_KEY"},
		StreamJSON:     true,
	})
}
