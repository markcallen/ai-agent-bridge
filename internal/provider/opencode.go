package provider

import "time"

// NewOpenCodeProvider creates the interactive PTY-backed OpenCode provider.
func NewOpenCodeProvider() *StdioProvider {
	return NewStdioProvider(StdioConfig{
		ProviderID:     "opencode",
		Binary:         "opencode",
		DefaultArgs:    nil,
		StartupTimeout: 45 * time.Second,
		StopGrace:      10 * time.Second,
		RequiredEnv:    []string{"OPENAI_API_KEY"},
		PromptPattern:  `❯`,
	})
}
