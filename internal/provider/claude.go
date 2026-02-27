package provider

import "time"

// NewClaudeProvider creates a provider adapter for Anthropic Claude CLI.
func NewClaudeProvider() *StdioProvider {
	return NewStdioProvider(StdioConfig{
		ProviderID:     "claude",
		Binary:         "claude",
		DefaultArgs:    []string{"--output-format", "stream-json", "--verbose"},
		StreamJSON:     true,
		StartupTimeout: 30 * time.Second,
		StopGrace:      10 * time.Second,
	})
}
