package provider

import "time"

// NewCodexProvider creates a provider adapter for OpenAI Codex CLI.
func NewCodexProvider() *StdioProvider {
	return NewStdioProvider(StdioConfig{
		ProviderID:     "codex",
		Binary:         "codex",
		DefaultArgs:    []string{"--quiet"},
		StartupTimeout: 30 * time.Second,
		StopGrace:      10 * time.Second,
	})
}
