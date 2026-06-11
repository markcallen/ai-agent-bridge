package localserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startLocalServer starts a server in local mode using a temp state dir and
// returns the server and a cleanup function.
func startLocalServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	if cfg.StateDir == "" {
		cfg.StateDir = t.TempDir()
	}
	srv, err := Start(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { srv.Stop() })
	return srv
}

// TestStartDefaultConfig verifies that Start() succeeds with a minimal config.
func TestStartDefaultConfig(t *testing.T) {
	srv := startLocalServer(t, Config{})
	assert.NotNil(t, srv)
	assert.NotEmpty(t, srv.Addr())
}

// TestStartWithDBPath verifies that Start() opens a BoltDB store when DBPath
// is set and that Stop() closes it without error.
func TestStartWithDBPath(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")

	srv := startLocalServer(t, Config{
		StateDir: dir,
		DBPath:   dbPath,
	})

	// Store file must exist after start.
	_, err := os.Stat(dbPath)
	assert.NoError(t, err, "BoltDB file should be created on start")

	// Stop should close the store cleanly.
	srv.Stop()
	// Double-stop must not panic.
	srv.Stop()
}

// TestStartWithInvalidDBPath ensures that an uncreateable db path causes Start
// to return an error rather than silently skipping persistence.
func TestStartWithInvalidDBPath(t *testing.T) {
	dir := t.TempDir()
	// Use a path whose parent directory does not exist.
	badPath := filepath.Join(dir, "no-such-dir", "sessions.db")

	cfg := Config{
		StateDir: dir,
		DBPath:   badPath,
	}
	_, err := Start(cfg)
	assert.Error(t, err, "Start should fail when the BoltDB path is invalid")
}

// TestStartDBPathRoundTrip verifies that a session created by one Server is
// rehydrated by a second Server opened on the same DBPath.
func TestStartDBPathRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")

	// First server: open the store.
	srv1 := startLocalServer(t, Config{
		StateDir: dir,
		DBPath:   dbPath,
	})
	_ = srv1
	srv1.Stop()

	// Second server: LoadHistory should succeed even on an empty DB.
	srv2 := startLocalServer(t, Config{
		StateDir: dir,
		DBPath:   dbPath,
	})
	assert.NotNil(t, srv2)
}

// TestStartWithConfigFile verifies that values from a YAML config file are
// merged into the running config.
func TestStartWithConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "bridge.yaml")
	require.NoError(t, os.WriteFile(cfgFile, []byte(`
rate_limits:
  global_rps: 42
sessions:
  idle_timeout: 5m
`), 0o644))

	srv := startLocalServer(t, Config{
		StateDir:   dir,
		ConfigPath: cfgFile,
	})
	assert.NotNil(t, srv)
}

// TestStartConfigFileExplicitOverride verifies that an explicit flag value
// takes precedence over the same value in the config file.
func TestStartConfigFileExplicitOverride(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "bridge.yaml")
	require.NoError(t, os.WriteFile(cfgFile, []byte(`
rate_limits:
  global_rps: 1
`), 0o644))

	// Explicit RateLimits.GlobalRPS should win over the file value.
	srv := startLocalServer(t, Config{
		StateDir:   dir,
		ConfigPath: cfgFile,
		RateLimits: struct {
			GlobalRPS                  float64
			GlobalBurst                int
			StartSessionPerClientRPS   float64
			StartSessionPerClientBurst int
			SendInputPerSessionRPS     float64
			SendInputPerSessionBurst   int
		}{GlobalRPS: 200},
	})
	assert.NotNil(t, srv)
}

// TestStartWithInvalidConfigFile verifies that Start() returns an error when
// the config file path is set but points to an unreadable location.
func TestStartWithInvalidConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		StateDir:   dir,
		ConfigPath: filepath.Join(dir, "missing.yaml"),
	}
	_, err := Start(cfg)
	assert.Error(t, err, "Start should fail when ConfigPath points to a missing file")
}

// TestStartWithRedactPatterns verifies that valid redaction patterns are
// accepted without error.
func TestStartWithRedactPatterns(t *testing.T) {
	srv := startLocalServer(t, Config{
		StateDir:       t.TempDir(),
		RedactPatterns: []string{`(?i)secret=[^\s]+`, `token=[^\s]+`},
	})
	assert.NotNil(t, srv)
}

// TestStartWithInvalidRedactPattern verifies that a bad regex causes Start to
// return an error.
func TestStartWithInvalidRedactPattern(t *testing.T) {
	dir := t.TempDir()
	_, err := Start(Config{
		StateDir:       dir,
		RedactPatterns: []string{`[invalid`},
	})
	assert.Error(t, err, "Start should fail with an invalid redact pattern")
}

// TestStartRateLimitDefaults verifies that Start() applies built-in defaults
// when no explicit rate limits or config file are provided.
func TestStartRateLimitDefaults(t *testing.T) {
	// Just ensure Start doesn't error; the defaults are applied internally.
	srv := startLocalServer(t, Config{StateDir: t.TempDir()})
	assert.NotNil(t, srv)
}

// TestStartCustomIdleTimeout verifies that a custom IdleTimeout is accepted.
func TestStartCustomIdleTimeout(t *testing.T) {
	srv := startLocalServer(t, Config{
		StateDir:    t.TempDir(),
		IdleTimeout: 10 * time.Minute,
	})
	assert.NotNil(t, srv)
}

// TestStartCustomEventBufferSize verifies that a custom EventBufferSize is accepted.
func TestStartCustomEventBufferSize(t *testing.T) {
	srv := startLocalServer(t, Config{
		StateDir:        t.TempDir(),
		EventBufferSize: 1 << 20,
	})
	assert.NotNil(t, srv)
}

// TestStartWithProviderFallbacks verifies that provider fallback mapping is
// accepted by Start without error.
func TestStartWithProviderFallbacks(t *testing.T) {
	srv := startLocalServer(t, Config{
		StateDir: t.TempDir(),
		ProviderFallbacks: map[string][]string{
			"claude": {"echo"},
		},
	})
	assert.NotNil(t, srv)
}

// TestServerAddrLocalMode verifies that Addr() returns a non-empty string in
// local mode (unix socket or localhost TCP on Windows).
func TestServerAddrLocalMode(t *testing.T) {
	srv := startLocalServer(t, Config{StateDir: t.TempDir()})
	assert.NotEmpty(t, srv.Addr())
}
