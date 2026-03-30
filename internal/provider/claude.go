package provider

import "time"

// NewClaudeProvider creates the interactive PTY-backed Claude provider.
func NewClaudeProvider() *StdioProvider {
	return NewStdioProvider(StdioConfig{
		ProviderID:     "claude",
		Binary:         "claude",
		DefaultArgs:    []string{"--dangerously-skip-permissions", "--verbose"},
		StartupTimeout: 60 * time.Second,
		StopGrace:      10 * time.Second,
		RequiredEnv:    []string{"ANTHROPIC_API_KEY"},
		PromptPattern:  `(?m)(❯|\>\s*$)`,
	})
}
