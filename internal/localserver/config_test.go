package localserver

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// registeredProviders returns the provider IDs registered with the server.
// Uses the internal registry directly to avoid the HealthAll probe that
// Health/ListProviders RPCs trigger.
func registeredProviders(srv *Server) []string {
	return srv.registry.List()
}

// TestStartWithConfigPath verifies that Start loads providers from a YAML
// config file and that the declared provider is actually registered.
func TestStartWithConfigPath(t *testing.T) {
	stateDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "bridge.yaml")

	yaml := `
providers:
  testprovider:
    binary: "cat"
    startup_probe: "none"
`
	require.NoError(t, os.WriteFile(configPath, []byte(yaml), 0o644))

	srv, err := Start(Config{
		StateDir:   stateDir,
		ConfigPath: configPath,
		Logger:     testLogger(),
	})
	require.NoError(t, err)
	defer srv.Stop()

	assert.Contains(t, registeredProviders(srv), "testprovider")
}

// TestStartWithMissingConfigPath verifies that Start succeeds when ConfigPath
// points to a non-existent file (missing file is silently ignored).
func TestStartWithMissingConfigPath(t *testing.T) {
	stateDir := t.TempDir()
	// Use a path inside a temp dir whose subdirectory was never created —
	// guaranteed missing without relying on any fixed absolute path.
	missingPath := filepath.Join(t.TempDir(), "subdir", "bridge.yaml")

	srv, err := Start(Config{
		StateDir:   stateDir,
		ConfigPath: missingPath,
		Logger:     testLogger(),
	})
	require.NoError(t, err)
	defer srv.Stop()
}

// TestStartWithEmptyConfigPath verifies that Start behaves identically when
// ConfigPath is empty (no config file loading attempted).
func TestStartWithEmptyConfigPath(t *testing.T) {
	stateDir := t.TempDir()

	srv, err := Start(Config{
		StateDir:   stateDir,
		ConfigPath: "",
		Logger:     testLogger(),
	})
	require.NoError(t, err)
	defer srv.Stop()
}

// TestStartConfigProviderOverridesAutoDetect verifies that a provider declared
// in the config file takes precedence over an auto-detected provider with the
// same ID. It creates a fake executable named after a known provider on PATH,
// then declares the same ID in config with a different binary (cat), and
// asserts the server registers it exactly once.
func TestStartConfigProviderOverridesAutoDetect(t *testing.T) {
	// Create a temp bin dir with a fake "codex" executable so detectProviders
	// will auto-detect it.
	binDir := t.TempDir()
	fakeBin := filepath.Join(binDir, "codex")
	require.NoError(t, os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fmt.Sprintf("%s:%s", binDir, origPath))

	stateDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "bridge.yaml")

	// Declare "codex" in config — registered first, so the auto-detected
	// duplicate must be skipped.
	yaml := `
providers:
  codex:
    binary: "cat"
    startup_probe: "none"
`
	require.NoError(t, os.WriteFile(configPath, []byte(yaml), 0o644))

	srv, err := Start(Config{
		StateDir:   stateDir,
		ConfigPath: configPath,
		Logger:     testLogger(),
	})
	require.NoError(t, err)
	defer srv.Stop()

	// "codex" must appear exactly once — config-declared version registered
	// first, auto-detected duplicate skipped.
	count := 0
	for _, id := range registeredProviders(srv) {
		if id == "codex" {
			count++
		}
	}
	assert.Equal(t, 1, count, "codex should be registered exactly once")
}

// TestStartWithInvalidConfigReturnsError verifies that Start returns an error
// when ConfigPath exists but contains invalid configuration.
func TestStartWithInvalidConfigReturnsError(t *testing.T) {
	stateDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "bridge.yaml")

	// Bad duration value triggers config.Validate failure.
	invalidYAML := `
sessions:
  idle_timeout: "not-a-duration"
`
	require.NoError(t, os.WriteFile(configPath, []byte(invalidYAML), 0o644))

	_, err := Start(Config{
		StateDir:   stateDir,
		ConfigPath: configPath,
		Logger:     testLogger(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load config")
}
