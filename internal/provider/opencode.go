package provider

import "time"

// NewOpenCodeProvider creates a provider adapter for the OpenCode CLI.
func NewOpenCodeProvider() *StdioProvider {
	return NewStdioProvider(StdioConfig{
		ProviderID:     "opencode",
		Binary:         "opencode",
		DefaultArgs:    nil,
		StartupTimeout: 30 * time.Second,
		StopGrace:      10 * time.Second,
	})
}
