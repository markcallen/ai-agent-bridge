package provider

import "time"

// NewClaudeProvider creates the interactive PTY-backed Claude provider.
func NewClaudeProvider() *StdioProvider {
	return NewStdioProvider(StdioConfig{
		ProviderID:     "claude",
		Binary:         "claude",
		DefaultArgs:    []string{"--verbose"},
		StartupTimeout: 60 * time.Second,
		StopGrace:      10 * time.Second,
		StartupProbe:   "prompt",
		RequiredEnv:    []string{"ANTHROPIC_AUTH_TOKEN"},
		PromptPattern:  `(?m)(❯|\>\s*$)`,
	})
}
