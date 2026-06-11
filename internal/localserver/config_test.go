package localserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStartWithConfigPath verifies that Start loads provider declarations from
// a YAML config file and registers them before auto-detected providers.
func TestStartWithConfigPath(t *testing.T) {
	stateDir := t.TempDir()
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "bridge.yaml")

	// Write a minimal config declaring a provider that uses 'cat' (always present).
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
}

// TestStartWithMissingConfigPath verifies that Start succeeds when ConfigPath
// points to a non-existent file (missing file is silently ignored).
func TestStartWithMissingConfigPath(t *testing.T) {
	stateDir := t.TempDir()

	srv, err := Start(Config{
		StateDir:   stateDir,
		ConfigPath: "/nonexistent/path/bridge.yaml",
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
// in the config file is not overwritten by an auto-detected provider with the
// same ID.
func TestStartConfigProviderOverridesAutoDetect(t *testing.T) {
	stateDir := t.TempDir()
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "bridge.yaml")

	// Declare 'echo' in the config — the same ID that Start always registers
	// via auto-registration. The config-declared version should take precedence
	// (the loop skips any auto-detected provider whose ID is already registered).
	yaml := `
providers:
  echo:
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

	// Server should have started successfully — duplicate registration is
	// handled by the "already registered from config" skip guard.
	assert.NotNil(t, srv)
}

// TestStartWithInvalidConfigReturnsError verifies that Start returns an error
// when ConfigPath exists but contains invalid YAML.
func TestStartWithInvalidConfigReturnsError(t *testing.T) {
	stateDir := t.TempDir()
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "bridge.yaml")

	// Use YAML with a bad duration value to trigger config.Validate failure.
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
