package cli_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/internal/localserver"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cliBinary holds the path to the compiled bridgectl binary.
// It is built once per test run via TestMain.
var cliBinary string

func TestMain(m *testing.M) {
	// Build the bridgectl binary into a temp dir.
	dir, err := os.MkdirTemp("", "cli-e2e-*")
	if err != nil {
		panic(err)
	}

	bin := filepath.Join(dir, "bridgectl")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/bridgectl")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic("failed to build bridgectl binary: " + err.Error())
	}
	cliBinary = bin

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// testStateDir returns a per-test temp state dir (isolated from ~/.ai-agent-bridge).
func testStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AI_AGENT_BRIDGE_STATE_DIR", dir)
	return dir
}

// TestServerStartStop verifies that the server starts and stops cleanly.
func TestServerStartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	stateDir := testStateDir(t)

	// Start server via the localserver package directly.
	srv, err := localserver.Start(localserver.Config{
		StateDir: stateDir,
	})
	require.NoError(t, err, "server should start")
	defer srv.Stop()

	// Verify server is discoverable.
	target, _ := localserver.DiscoverTarget(stateDir)
	require.NotEmpty(t, target, "should discover running server")

	// Health check via SDK.
	client, err := bridgeclient.New(bridgeclient.WithTarget(target))
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := client.Health(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.ServerInstanceId)

	// Stop server.
	srv.Stop()

	// Verify server is no longer discoverable.
	assert.False(t, localserver.IsServerRunning(stateDir))
}

// TestEchoSessionLifecycle tests creating, listing, and stopping a session
// using the echo (cat) provider.
func TestEchoSessionLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	stateDir := testStateDir(t)
	repoDir := t.TempDir()

	srv, err := localserver.Start(localserver.Config{
		StateDir: stateDir,
	})
	require.NoError(t, err)
	defer srv.Stop()

	target := srv.Target()
	client, err := bridgeclient.New(bridgeclient.WithTarget(target))
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	client.SetProject("test")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start a session.
	sessionID := uuid.NewString()
	startResp, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   "test",
		SessionId:   sessionID,
		RepoPath:    repoDir,
		Provider:    "echo",
		InitialCols: 80,
		InitialRows: 24,
	})
	require.NoError(t, err)
	assert.Equal(t, sessionID, startResp.SessionId)

	// Wait for the session to be running.
	var info *bridgev1.GetSessionResponse
	for i := 0; i < 20; i++ {
		info, err = client.GetSession(ctx, &bridgev1.GetSessionRequest{
			SessionId: sessionID,
		})
		if err == nil && info.Status != bridgev1.SessionStatus_SESSION_STATUS_STARTING {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NoError(t, err)
	assert.Equal(t, sessionID, info.SessionId)

	// List sessions.
	listResp, err := client.ListSessions(ctx, &bridgev1.ListSessionsRequest{
		ProjectId: "test",
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(listResp.Sessions), 1)
	found := false
	for _, s := range listResp.Sessions {
		if s.SessionId == sessionID {
			found = true
		}
	}
	assert.True(t, found, "session should appear in list")

	// WriteInput may fail if no client is attached — that's OK for this test.
	// The important thing is the session is running.
	_, _ = client.WriteInput(ctx, &bridgev1.WriteInputRequest{
		SessionId: sessionID,
		ClientId:  "test-client",
		Data:      []byte("hello\n"),
	})

	// Stop session.
	_, err = client.StopSession(ctx, &bridgev1.StopSessionRequest{
		SessionId: sessionID,
		Force:     true,
	})
	require.NoError(t, err)

	// Verify session is stopped.
	time.Sleep(200 * time.Millisecond)
	info, err = client.GetSession(ctx, &bridgev1.GetSessionRequest{
		SessionId: sessionID,
	})
	require.NoError(t, err)
	assert.True(t,
		info.Status == bridgev1.SessionStatus_SESSION_STATUS_STOPPED ||
			info.Status == bridgev1.SessionStatus_SESSION_STATUS_FAILED,
		"session should be stopped or failed, got %v", info.Status)
}

// TestAutoServerDiscovery tests that a second client discovers the first server.
func TestAutoServerDiscovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	stateDir := testStateDir(t)

	// Start first server.
	srv, err := localserver.Start(localserver.Config{
		StateDir: stateDir,
	})
	require.NoError(t, err)
	defer srv.Stop()

	// The second "instance" should discover the existing server.
	target, _ := localserver.DiscoverTarget(stateDir)
	require.NotEmpty(t, target, "second instance should discover existing server")
	assert.Equal(t, srv.Target(), target, "should discover the same server")

	// Verify health from second connection.
	client, err := bridgeclient.New(bridgeclient.WithTarget(target))
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := client.Health(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.ServerInstanceId)
}

// TestMultipleSessionsSameServer tests that multiple sessions can run on the
// same server (simulating multiple terminal windows).
func TestMultipleSessionsSameServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	stateDir := testStateDir(t)
	repoDir := t.TempDir()

	srv, err := localserver.Start(localserver.Config{
		StateDir: stateDir,
	})
	require.NoError(t, err)
	defer srv.Stop()

	target := srv.Target()
	client, err := bridgeclient.New(bridgeclient.WithTarget(target))
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	client.SetProject("test")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start two sessions.
	sessionA := uuid.NewString()
	sessionB := uuid.NewString()
	for _, id := range []string{sessionA, sessionB} {
		_, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
			ProjectId:   "test",
			SessionId:   id,
			RepoPath:    repoDir,
			Provider:    "echo",
			InitialCols: 80,
			InitialRows: 24,
		})
		require.NoError(t, err, "should start session %s", id)
	}

	// Wait briefly for sessions to start.
	time.Sleep(500 * time.Millisecond)

	// List sessions — both should be present.
	listResp, err := client.ListSessions(ctx, &bridgev1.ListSessionsRequest{
		ProjectId: "test",
	})
	require.NoError(t, err)
	ids := make(map[string]bool)
	for _, s := range listResp.Sessions {
		ids[s.SessionId] = true
	}
	assert.True(t, ids[sessionA], "session-a should be listed")
	assert.True(t, ids[sessionB], "session-b should be listed")

	// Stop both.
	for _, id := range []string{sessionA, sessionB} {
		_, err := client.StopSession(ctx, &bridgev1.StopSessionRequest{
			SessionId: id,
			Force:     true,
		})
		require.NoError(t, err)
	}
}

// TestServerDoesNotDoubleStart verifies that discovery finds the already-running
// server, so a second caller would connect to it instead of starting a new one.
func TestServerDoesNotDoubleStart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	stateDir := testStateDir(t)

	srv1, err := localserver.Start(localserver.Config{
		StateDir: stateDir,
	})
	require.NoError(t, err)
	defer srv1.Stop()

	// Detect that server is running.
	assert.True(t, localserver.IsServerRunning(stateDir))

	// Discovery should find the existing server.
	target, _ := localserver.DiscoverTarget(stateDir)
	require.NotEmpty(t, target)
	assert.Equal(t, srv1.Target(), target)
}

// TestCLIVersion tests that `bridgectl --version` works.
func TestCLIVersion(t *testing.T) {
	cmd := exec.Command(cliBinary, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	require.NoError(t, err, "--version should succeed")
	assert.Contains(t, out.String(), "bridgectl version")
}

// TestCLIHelp tests that `bridgectl --help` exits cleanly.
func TestCLIHelp(t *testing.T) {
	cmd := exec.Command(cliBinary, "--help")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	require.NoError(t, err, "--help should succeed")
	assert.Contains(t, out.String(), "bridgectl starts a local bridge server")
}

// TestCLISessionListNoServer tests that `session list` handles no server gracefully.
func TestCLISessionListNoServer(t *testing.T) {
	stateDir := testStateDir(t)

	cmd := exec.Command(cliBinary, "session", "list")
	cmd.Env = append(os.Environ(), "AI_AGENT_BRIDGE_STATE_DIR="+stateDir)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	// Should succeed but print "No ai-agent-bridge server running."
	require.NoError(t, err)
	assert.Contains(t, out.String(), "No ai-agent-bridge server running")
}

// TestCLIServerStatus tests `server status` output.
func TestCLIServerStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	stateDir := testStateDir(t)

	// No server running.
	cmd := exec.Command(cliBinary, "server", "status")
	cmd.Env = append(os.Environ(), "AI_AGENT_BRIDGE_STATE_DIR="+stateDir)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	require.NoError(t, err)
	assert.Contains(t, out.String(), "not running")

	// Start server.
	srv, err := localserver.Start(localserver.Config{
		StateDir: stateDir,
	})
	require.NoError(t, err)
	defer srv.Stop()

	// Now status should show running.
	cmd = exec.Command(cliBinary, "server", "status")
	cmd.Env = append(os.Environ(), "AI_AGENT_BRIDGE_STATE_DIR="+stateDir)
	out.Reset()
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	require.NoError(t, err)
	output := out.String()
	assert.Contains(t, output, "running")
}

// TestProviderEchoAvailable verifies the echo provider shows as available.
func TestProviderEchoAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	stateDir := testStateDir(t)
	srv, err := localserver.Start(localserver.Config{
		StateDir: stateDir,
	})
	require.NoError(t, err)
	defer srv.Stop()

	client, err := bridgeclient.New(bridgeclient.WithTarget(srv.Target()))
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.Health(ctx)
	require.NoError(t, err)

	found := false
	for _, p := range resp.Providers {
		if p.Provider == "echo" {
			found = true
			assert.True(t, p.Available, "echo provider should be available")
		}
	}
	assert.True(t, found, "echo provider should be listed in health response")
}

// TestCleanShutdownCleansFiles verifies state files are removed on stop.
func TestCleanShutdownCleansFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	stateDir := testStateDir(t)
	srv, err := localserver.Start(localserver.Config{
		StateDir: stateDir,
	})
	require.NoError(t, err)

	// Files should exist.
	_, err = os.Stat(filepath.Join(stateDir, "server.pid"))
	assert.NoError(t, err, "PID file should exist")
	_, err = os.Stat(filepath.Join(stateDir, "server.addr"))
	assert.NoError(t, err, "addr file should exist")

	if runtime.GOOS != "windows" {
		_, err = os.Stat(filepath.Join(stateDir, "server.sock"))
		assert.NoError(t, err, "socket should exist")
	}

	srv.Stop()

	// Files should be cleaned up.
	_, err = os.Stat(filepath.Join(stateDir, "server.pid"))
	assert.True(t, os.IsNotExist(err), "PID file should be removed")
	_, err = os.Stat(filepath.Join(stateDir, "server.addr"))
	assert.True(t, os.IsNotExist(err), "addr file should be removed")
}

// TestStaleSocketRecovery verifies that a stale unix socket is cleaned up.
func TestStaleSocketRecovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets not used on Windows")
	}
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	stateDir := testStateDir(t)

	// Create a stale socket file.
	sockPath := filepath.Join(stateDir, "server.sock")
	if err := os.WriteFile(sockPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	// IsServerRunning should return false (stale socket, no listener).
	assert.False(t, localserver.IsServerRunning(stateDir))

	// Start should succeed by replacing the stale socket.
	srv, err := localserver.Start(localserver.Config{
		StateDir: stateDir,
	})
	require.NoError(t, err)
	defer srv.Stop()

	assert.True(t, localserver.IsServerRunning(stateDir))
}

// TestSessionAttachAndInput tests attaching to a session and writing input.
func TestSessionAttachAndInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	stateDir := testStateDir(t)
	repoDir := t.TempDir()

	srv, err := localserver.Start(localserver.Config{
		StateDir: stateDir,
	})
	require.NoError(t, err)
	defer srv.Stop()

	target := srv.Target()
	client, err := bridgeclient.New(bridgeclient.WithTarget(target))
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	client.SetProject("test")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sessionID := uuid.NewString()
	_, err = client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   "test",
		SessionId:   sessionID,
		RepoPath:    repoDir,
		Provider:    "echo",
		InitialCols: 80,
		InitialRows: 24,
	})
	require.NoError(t, err)

	// Wait for session to be running.
	time.Sleep(500 * time.Millisecond)

	// Attach and read output in a goroutine (RecvAll opens the gRPC stream).
	clientID := uuid.NewString()
	stream, err := client.AttachSession(ctx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  clientID,
		AfterSeq:  0,
	})
	require.NoError(t, err)

	var received strings.Builder
	var mu sync.Mutex
	readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readCancel()

	attached := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = stream.RecvAll(readCtx, func(ev *bridgev1.AttachSessionEvent) error {
			switch ev.Type {
			case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ATTACHED:
				select {
				case attached <- struct{}{}:
				default:
				}
			case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_OUTPUT:
				mu.Lock()
				received.Write(ev.Payload)
				got := received.String()
				mu.Unlock()
				if strings.Contains(got, "HELLO_FROM_E2E") {
					readCancel()
				}
			}
			return nil
		})
	}()

	// Wait for the attach event before writing input.
	select {
	case <-attached:
	case <-time.After(3 * time.Second):
		t.Log("timeout waiting for attach event, trying write anyway")
	}

	// Write some input. The echo provider (cat) echoes it back.
	testMsg := "HELLO_FROM_E2E\n"
	_, err = client.WriteInput(ctx, &bridgev1.WriteInputRequest{
		SessionId: sessionID,
		ClientId:  stream.ClientID(),
		Data:      []byte(testMsg),
	})
	require.NoError(t, err)

	// Wait for output — require it to arrive.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}

	mu.Lock()
	got := received.String()
	mu.Unlock()
	require.NotEmpty(t, got, "echo provider should return output")
	assert.Contains(t, got, "HELLO_FROM_E2E")

	// Stop.
	_, err = client.StopSession(ctx, &bridgev1.StopSessionRequest{
		SessionId: sessionID,
		Force:     true,
	})
	require.NoError(t, err)
}

// --- Tier-1 auto-PKI tests (Section 2 of test plan) ---

// TestAutoPKIGeneratesAllFiles verifies that starting a secure-mode server
// with no Step CA flags generates the full set of PKI files.
func TestAutoPKIGeneratesAllFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("secure mode not supported on Windows")
	}

	stateDir := testStateDir(t)

	srv, err := localserver.Start(localserver.Config{
		StateDir:   stateDir,
		ListenAddr: "127.0.0.1:0",
		ServerSANs: []string{"127.0.0.1"},
	})
	require.NoError(t, err, "secure server should start")
	defer srv.Stop()

	certsDir := filepath.Join(stateDir, "certs")
	expectedFiles := []string{
		"ca.crt",
		"ca.key",
		"server.crt",
		"server.key",
		"local-client.crt",
		"local-client.key",
		"ca-bundle.crt",
		"jwt-signing.key",
		"jwt-signing.pub",
	}
	for _, name := range expectedFiles {
		path := filepath.Join(certsDir, name)
		_, err := os.Stat(path)
		assert.NoError(t, err, "PKI file should exist: %s", name)
	}

	// Private keys should have restricted permissions (0600).
	privateKeys := []string{"ca.key", "server.key", "local-client.key", "jwt-signing.key"}
	for _, name := range privateKeys {
		info, err := os.Stat(filepath.Join(certsDir, name))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
			"private key %s should be 0600", name)
	}

	// Health check should succeed with the auto-generated creds.
	target, _ := localserver.DiscoverTarget(stateDir)
	client := secureClient(t, target, stateDir)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := client.Health(ctx)
	require.NoError(t, err, "health check should succeed with auto-PKI creds")
	assert.NotEmpty(t, resp.ServerInstanceId)
}

// TestAutoPKIIdempotentAcrossRestart verifies that stopping and restarting
// a secure-mode server does not regenerate certificates.
func TestAutoPKIIdempotentAcrossRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("secure mode not supported on Windows")
	}

	stateDir := testStateDir(t)

	// First start — generates PKI.
	srv1, err := localserver.Start(localserver.Config{
		StateDir:   stateDir,
		ListenAddr: "127.0.0.1:0",
		ServerSANs: []string{"127.0.0.1"},
	})
	require.NoError(t, err)

	certsDir := filepath.Join(stateDir, "certs")
	bundlePath := filepath.Join(certsDir, "ca-bundle.crt")
	caPath := filepath.Join(certsDir, "ca.crt")

	// Record contents from first start.
	bundle1, err := os.ReadFile(bundlePath)
	require.NoError(t, err)
	ca1, err := os.ReadFile(caPath)
	require.NoError(t, err)

	srv1.Stop()

	// Second start — should reuse existing certs.
	srv2, err := localserver.Start(localserver.Config{
		StateDir:   stateDir,
		ListenAddr: "127.0.0.1:0",
		ServerSANs: []string{"127.0.0.1"},
	})
	require.NoError(t, err)
	defer srv2.Stop()

	bundle2, err := os.ReadFile(bundlePath)
	require.NoError(t, err)
	ca2, err := os.ReadFile(caPath)
	require.NoError(t, err)

	assert.Equal(t, bundle1, bundle2, "ca-bundle.crt should not be regenerated")
	assert.Equal(t, ca1, ca2, "ca.crt should not be regenerated")

	// Server should still be fully functional with the original certs.
	target, _ := localserver.DiscoverTarget(stateDir)
	client := secureClient(t, target, stateDir)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := client.Health(ctx)
	require.NoError(t, err, "health check should pass after restart with same certs")
	assert.NotEmpty(t, resp.ServerInstanceId)
}

// TestIssuedClientCertCanConnect verifies that a client using credentials
// from IssueClientCert can connect to and authenticate with the server.
func TestIssuedClientCertCanConnect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("secure mode not supported on Windows")
	}

	stateDir := testStateDir(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Start and stop a server to generate PKI.
	srv1, err := localserver.Start(localserver.Config{
		StateDir:   stateDir,
		ListenAddr: "127.0.0.1:0",
		ServerSANs: []string{"127.0.0.1"},
	})
	require.NoError(t, err)
	srv1.Stop()

	// Issue a client certificate. This writes the JWT public key to
	// certs/jwt-clients/ which the server reads at startup.
	clientName := "sdk-test"
	certPath, keyPath, err := localserver.IssueClientCert(stateDir, clientName, logger)
	require.NoError(t, err, "should issue client cert")

	// Verify expected files were created.
	clientDir := filepath.Join(stateDir, "certs", "clients", clientName)
	assert.Equal(t, filepath.Join(clientDir, clientName+".crt"), certPath)
	assert.Equal(t, filepath.Join(clientDir, clientName+".key"), keyPath)
	_, err = os.Stat(filepath.Join(clientDir, "jwt-signing.key"))
	require.NoError(t, err, "per-client JWT key should exist")
	_, err = os.Stat(filepath.Join(stateDir, "certs", "jwt-clients", clientName+".pub"))
	require.NoError(t, err, "server-side JWT pub should be registered")

	// Restart server so it loads the new JWT public key.
	srv2, err := localserver.Start(localserver.Config{
		StateDir:   stateDir,
		ListenAddr: "127.0.0.1:0",
		ServerSANs: []string{"127.0.0.1"},
	})
	require.NoError(t, err)
	defer srv2.Stop()

	target, _ := localserver.DiscoverTarget(stateDir)

	// Connect using the issued client credentials (not local-client).
	mat := localserver.LoadPKIMaterial(stateDir)
	issuedClient, err := bridgeclient.New(
		bridgeclient.WithTarget(target),
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: mat.CABundlePath,
			CertPath:     certPath,
			KeyPath:      keyPath,
			ServerName:   "server",
		}),
		bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: filepath.Join(clientDir, "jwt-signing.key"),
			Issuer:         clientName,
			Audience:       "bridge",
		}),
	)
	require.NoError(t, err)
	defer func() { _ = issuedClient.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := issuedClient.Health(ctx)
	require.NoError(t, err, "issued client should authenticate successfully")
	assert.NotEmpty(t, resp.ServerInstanceId)
}

// TestClientNameValidation verifies that IssueClientCert rejects invalid
// names (path traversal, special characters) and accepts valid ones.
func TestClientNameValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("secure mode not supported on Windows")
	}

	stateDir := testStateDir(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Generate PKI so IssueClientCert has a CA to sign with.
	srv, err := localserver.Start(localserver.Config{
		StateDir:   stateDir,
		ListenAddr: "127.0.0.1:0",
		ServerSANs: []string{"127.0.0.1"},
	})
	require.NoError(t, err)
	defer srv.Stop()

	// Invalid names should be rejected.
	invalidNames := []struct {
		name   string
		reason string
	}{
		{"../escape", "path traversal"},
		{"foo/bar", "slash in name"},
		{".hidden", "leading dot"},
		{"", "empty string"},
		{"a b c", "spaces"},
	}
	for _, tc := range invalidNames {
		_, _, err := localserver.IssueClientCert(stateDir, tc.name, logger)
		assert.Error(t, err, "should reject %q (%s)", tc.name, tc.reason)
	}

	// Valid names should be accepted.
	validNames := []string{"a", "valid-client_1.0", "laptop2", "dev-machine", "server.local"}
	for _, name := range validNames {
		_, _, err := localserver.IssueClientCert(stateDir, name, logger)
		assert.NoError(t, err, "should accept %q", name)
	}
}

// --- Step CA flag validation tests (Section 3 of test plan) ---

// TestStepCAMissingRoot verifies that starting a server with --step-ca-url
// but without --step-ca-root returns a clear error.
func TestStepCAMissingRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("secure mode not supported on Windows")
	}

	stateDir := testStateDir(t)

	_, err := localserver.Start(localserver.Config{
		StateDir:   stateDir,
		ListenAddr: "127.0.0.1:0",
		ServerSANs: []string{"127.0.0.1"},
		StepCAURL:  "https://ca.example.com",
		// StepCARootPath intentionally omitted
	})
	require.Error(t, err, "should fail when --step-ca-root is missing")
	assert.Contains(t, err.Error(), "step-ca-root is required",
		"error should mention the missing flag")
}

// TestStepCANonexistentRoot verifies that a nonexistent --step-ca-root path
// produces a clear error about the missing file.
func TestStepCANonexistentRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("secure mode not supported on Windows")
	}

	stateDir := testStateDir(t)

	_, err := localserver.Start(localserver.Config{
		StateDir:       stateDir,
		ListenAddr:     "127.0.0.1:0",
		ServerSANs:     []string{"127.0.0.1"},
		StepCAURL:      "https://ca.example.com",
		StepCARootPath: "/nonexistent/root.crt",
	})
	require.Error(t, err, "should fail when root cert file does not exist")
	assert.Contains(t, err.Error(), "copy Step CA root",
		"error should mention the copy failure")
}

// TestStepCAMissingStepCLI verifies that when `step` is not on PATH,
// the server returns a clear error with an install link.
func TestStepCAMissingStepCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("secure mode not supported on Windows")
	}

	stateDir := testStateDir(t)

	// Create a dummy root cert file so the copy step succeeds.
	dummyRoot := filepath.Join(t.TempDir(), "root.crt")
	require.NoError(t, os.WriteFile(dummyRoot, []byte("dummy-cert"), 0o644))

	// Remove step from PATH so exec.LookPath fails.
	t.Setenv("PATH", t.TempDir())

	_, err := localserver.Start(localserver.Config{
		StateDir:       stateDir,
		ListenAddr:     "127.0.0.1:0",
		ServerSANs:     []string{"127.0.0.1"},
		StepCAURL:      "https://ca.example.com",
		StepCARootPath: dummyRoot,
	})
	require.Error(t, err, "should fail when step CLI is not on PATH")
	assert.Contains(t, err.Error(), "step",
		"error should mention the step CLI")
	assert.Contains(t, err.Error(), "smallstep.com/cli",
		"error should include the install URL")
}

// TestOIDCFlagValidation verifies that IssueClientCertViaOIDC rejects
// incomplete flag combinations with clear error messages.
func TestOIDCFlagValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("secure mode not supported on Windows")
	}

	stateDir := testStateDir(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	tests := []struct {
		name    string
		client  string
		stepCA  *localserver.StepCAConfig
		wantErr string
	}{
		{
			name:    "missing step-ca-url",
			client:  "alice",
			stepCA:  nil,
			wantErr: "step-ca-url",
		},
		{
			name:   "missing oidc-provider",
			client: "alice",
			stepCA: &localserver.StepCAConfig{
				URL:      "https://ca.example.com",
				RootPath: "/tmp/root.crt",
			},
			wantErr: "oidc-provider",
		},
		{
			name:   "missing step-ca-root",
			client: "alice",
			stepCA: &localserver.StepCAConfig{
				URL:             "https://ca.example.com",
				OIDCProviderURL: "https://accounts.google.com",
			},
			wantErr: "step-ca-root",
		},
		{
			name:   "invalid client name",
			client: "../escape",
			stepCA: &localserver.StepCAConfig{
				URL:             "https://ca.example.com",
				RootPath:        "/tmp/root.crt",
				OIDCProviderURL: "https://accounts.google.com",
			},
			wantErr: "invalid client name",
		},
		{
			name:   "step CLI not on PATH",
			client: "bob",
			stepCA: &localserver.StepCAConfig{
				URL:             "https://ca.example.com",
				RootPath:        "/tmp/root.crt",
				OIDCProviderURL: "https://accounts.google.com",
			},
			wantErr: "step",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Ensure step is not on PATH for the last test case.
			if tc.name == "step CLI not on PATH" {
				t.Setenv("PATH", t.TempDir())
			}
			_, _, err := localserver.IssueClientCertViaOIDC(stateDir, tc.client, tc.stepCA, logger)
			require.Error(t, err, "should fail for case: %s", tc.name)
			assert.Contains(t, err.Error(), tc.wantErr,
				"error for %q should mention %q", tc.name, tc.wantErr)
		})
	}
}

// TestOIDCMissingNameFlag verifies that the CLI rejects issue-client --oidc-provider
// when --name is not provided (Cobra flag validation).
func TestOIDCMissingNameFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("secure mode not supported on Windows")
	}

	stateDir := testStateDir(t)

	cmd := exec.Command(cliBinary, "server", "issue-client",
		"--oidc-provider", "https://accounts.google.com",
		"--step-ca-url", "https://ca.example.com",
		"--step-ca-root", "/tmp/root.crt",
	)
	cmd.Env = append(os.Environ(), "AI_AGENT_BRIDGE_STATE_DIR="+stateDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	require.Error(t, err, "should fail when --name is missing")
	assert.Contains(t, stderr.String(), "name",
		"error should mention the missing --name flag")
}

// --- Writer slot release tests (Section 4 of test plan) ---

// startEchoSession creates a session using the echo provider and waits for it
// to reach the running state. It returns the session ID.
func startEchoSession(t *testing.T, ctx context.Context, client *bridgeclient.Client, repoDir string) string {
	t.Helper()
	sessionID := uuid.NewString()
	_, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   "test",
		SessionId:   sessionID,
		RepoPath:    repoDir,
		Provider:    "echo",
		InitialCols: 80,
		InitialRows: 24,
	})
	require.NoError(t, err)
	// Wait for session to be running.
	time.Sleep(500 * time.Millisecond)
	return sessionID
}

// attachAndCollectEvents attaches to a session and collects events in the
// background. Returns the stream, a channel that signals when ATTACHED is
// received, and a function to retrieve collected events.
func attachAndCollectEvents(
	t *testing.T,
	ctx context.Context,
	client *bridgeclient.Client,
	sessionID, clientID string,
	role bridgev1.AttachRole,
) (stream *bridgeclient.OutputStream, attached <-chan struct{}, getEvents func() []*bridgev1.AttachSessionEvent, cancel context.CancelFunc) {
	t.Helper()
	recvCtx, recvCancel := context.WithCancel(ctx)

	stream, err := client.AttachSession(recvCtx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  clientID,
		AfterSeq:  0,
		Role:      role,
	})
	require.NoError(t, err)

	var mu sync.Mutex
	var events []*bridgev1.AttachSessionEvent
	attachedCh := make(chan struct{}, 1)

	go func() {
		_ = stream.RecvAll(recvCtx, func(ev *bridgev1.AttachSessionEvent) error {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
			if ev.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ATTACHED {
				select {
				case attachedCh <- struct{}{}:
				default:
				}
			}
			return nil
		})
	}()

	return stream, attachedCh, func() []*bridgev1.AttachSessionEvent {
		mu.Lock()
		defer mu.Unlock()
		cp := make([]*bridgev1.AttachSessionEvent, len(events))
		copy(cp, events)
		return cp
	}, recvCancel
}

// waitForAttach blocks until the attached channel fires or a timeout expires.
func waitForAttach(t *testing.T, attached <-chan struct{}) {
	t.Helper()
	select {
	case <-attached:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for attach event")
	}
}

// hasEventType returns true if the event slice contains an event of the given type.
func hasEventType(events []*bridgev1.AttachSessionEvent, typ bridgev1.AttachEventType) bool {
	for _, ev := range events {
		if ev.Type == typ {
			return true
		}
	}
	return false
}

// TestWriterReleasedOnDisconnect verifies that when the active writer
// disconnects, observers receive a WRITER_RELEASED event.
func TestWriterReleasedOnDisconnect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	stateDir := testStateDir(t)
	repoDir := t.TempDir()

	srv, err := localserver.Start(localserver.Config{StateDir: stateDir})
	require.NoError(t, err)
	defer srv.Stop()

	target := srv.Target()
	ctx, ctxCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer ctxCancel()

	// Create two clients sharing the same gRPC connection.
	client, err := bridgeclient.New(bridgeclient.WithTarget(target))
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	client.SetProject("test")

	sessionID := startEchoSession(t, ctx, client, repoDir)

	// Client A: attach as writer.
	writerID := uuid.NewString()
	_, writerAttached, _, writerCancel := attachAndCollectEvents(
		t, ctx, client, sessionID, writerID,
		bridgev1.AttachRole_ATTACH_ROLE_WRITER,
	)
	waitForAttach(t, writerAttached)

	// Client B: attach as observer.
	observerID := uuid.NewString()
	_, observerAttached, getObserverEvents, observerCancel := attachAndCollectEvents(
		t, ctx, client, sessionID, observerID,
		bridgev1.AttachRole_ATTACH_ROLE_OBSERVER,
	)
	defer observerCancel()
	waitForAttach(t, observerAttached)

	// Writer disconnects — cancel its stream context.
	writerCancel()
	// Give the server time to broadcast the WRITER_RELEASED event.
	time.Sleep(500 * time.Millisecond)

	// Observer should have received WRITER_RELEASED.
	events := getObserverEvents()
	assert.True(t, hasEventType(events, bridgev1.AttachEventType_ATTACH_EVENT_TYPE_WRITER_RELEASED),
		"observer should receive WRITER_RELEASED when writer disconnects; got events: %v", eventTypes(events))

	// Verify the WRITER_RELEASED event identifies the disconnected writer.
	for _, ev := range events {
		if ev.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_WRITER_RELEASED {
			assert.Equal(t, writerID, ev.WriterClientId,
				"WRITER_RELEASED should identify the disconnected writer")
		}
	}

	// Cleanup.
	_, _ = client.StopSession(ctx, &bridgev1.StopSessionRequest{
		SessionId: sessionID, Force: true,
	})
}

// TestWriterEvictionBroadcastsEvents verifies that force-claiming the writer
// slot broadcasts WRITER_RELEASED (for the evicted writer) and WRITER_CLAIMED
// (for the new writer) to all observers.
func TestWriterEvictionBroadcastsEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	stateDir := testStateDir(t)
	repoDir := t.TempDir()

	srv, err := localserver.Start(localserver.Config{StateDir: stateDir})
	require.NoError(t, err)
	defer srv.Stop()

	target := srv.Target()
	ctx, ctxCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer ctxCancel()

	client, err := bridgeclient.New(bridgeclient.WithTarget(target))
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	client.SetProject("test")

	sessionID := startEchoSession(t, ctx, client, repoDir)

	// Client A: attach as writer.
	writerAID := uuid.NewString()
	_, writerAAttached, getWriterAEvents, writerACancel := attachAndCollectEvents(
		t, ctx, client, sessionID, writerAID,
		bridgev1.AttachRole_ATTACH_ROLE_WRITER,
	)
	defer writerACancel()
	waitForAttach(t, writerAAttached)

	// Client B: attach as observer.
	observerID := uuid.NewString()
	_, observerAttached, getObserverEvents, observerCancel := attachAndCollectEvents(
		t, ctx, client, sessionID, observerID,
		bridgev1.AttachRole_ATTACH_ROLE_OBSERVER,
	)
	defer observerCancel()
	waitForAttach(t, observerAttached)

	// Client C: attach as observer first (ClaimWriter requires an attached client).
	claimantID := uuid.NewString()
	_, claimantAttached, _, claimantCancel := attachAndCollectEvents(
		t, ctx, client, sessionID, claimantID,
		bridgev1.AttachRole_ATTACH_ROLE_OBSERVER,
	)
	defer claimantCancel()
	waitForAttach(t, claimantAttached)

	// Client C: force-claim the writer slot, evicting Client A.
	claimResp, err := client.ClaimWriter(ctx, &bridgev1.ClaimWriterRequest{
		SessionId: sessionID,
		ClientId:  claimantID,
		Force:     true,
	})
	require.NoError(t, err)
	assert.True(t, claimResp.Claimed, "force claim should succeed")
	assert.Equal(t, writerAID, claimResp.PreviousWriterClientId,
		"should report the evicted writer")

	// Give the server time to broadcast events.
	time.Sleep(500 * time.Millisecond)

	// Observer (Client B) should see both WRITER_RELEASED and WRITER_CLAIMED.
	obsEvents := getObserverEvents()
	assert.True(t, hasEventType(obsEvents, bridgev1.AttachEventType_ATTACH_EVENT_TYPE_WRITER_RELEASED),
		"observer should receive WRITER_RELEASED for the evicted writer; got: %v", eventTypes(obsEvents))
	assert.True(t, hasEventType(obsEvents, bridgev1.AttachEventType_ATTACH_EVENT_TYPE_WRITER_CLAIMED),
		"observer should receive WRITER_CLAIMED for the new writer; got: %v", eventTypes(obsEvents))

	// Verify the event payloads identify the correct clients.
	for _, ev := range obsEvents {
		switch ev.Type {
		case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_WRITER_RELEASED:
			assert.Equal(t, writerAID, ev.WriterClientId,
				"WRITER_RELEASED should identify the evicted writer")
		case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_WRITER_CLAIMED:
			assert.Equal(t, claimantID, ev.WriterClientId,
				"WRITER_CLAIMED should identify the new writer")
		}
	}

	// Client A (evicted writer, now observer) should also see the events.
	writerAEvents := getWriterAEvents()
	assert.True(t, hasEventType(writerAEvents, bridgev1.AttachEventType_ATTACH_EVENT_TYPE_WRITER_RELEASED),
		"evicted writer should receive WRITER_RELEASED; got: %v", eventTypes(writerAEvents))
	assert.True(t, hasEventType(writerAEvents, bridgev1.AttachEventType_ATTACH_EVENT_TYPE_WRITER_CLAIMED),
		"evicted writer should receive WRITER_CLAIMED; got: %v", eventTypes(writerAEvents))

	// Cleanup.
	_, _ = client.StopSession(ctx, &bridgev1.StopSessionRequest{
		SessionId: sessionID, Force: true,
	})
}

// TestObserverClaimsWriterAfterRelease verifies that an observer can claim the
// writer slot after it is voluntarily released, and then write input.
func TestObserverClaimsWriterAfterRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	stateDir := testStateDir(t)
	repoDir := t.TempDir()

	srv, err := localserver.Start(localserver.Config{StateDir: stateDir})
	require.NoError(t, err)
	defer srv.Stop()

	target := srv.Target()
	ctx, ctxCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer ctxCancel()

	client, err := bridgeclient.New(bridgeclient.WithTarget(target))
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	client.SetProject("test")

	sessionID := startEchoSession(t, ctx, client, repoDir)

	// Client A: attach as writer.
	writerAID := uuid.NewString()
	_, writerAAttached, _, writerACancel := attachAndCollectEvents(
		t, ctx, client, sessionID, writerAID,
		bridgev1.AttachRole_ATTACH_ROLE_WRITER,
	)
	defer writerACancel()
	waitForAttach(t, writerAAttached)

	// Client B: attach as observer.
	observerID := uuid.NewString()
	observerStream, observerAttached, getObserverEvents, observerCancel := attachAndCollectEvents(
		t, ctx, client, sessionID, observerID,
		bridgev1.AttachRole_ATTACH_ROLE_OBSERVER,
	)
	defer observerCancel()
	waitForAttach(t, observerAttached)

	// Client A: voluntarily release the writer slot.
	releaseResp, err := client.ReleaseWriter(ctx, &bridgev1.ReleaseWriterRequest{
		SessionId: sessionID,
		ClientId:  writerAID,
	})
	require.NoError(t, err)
	assert.True(t, releaseResp.Released, "release should succeed")

	// Give the server time to broadcast WRITER_RELEASED.
	time.Sleep(300 * time.Millisecond)

	// Observer should have received the release notification.
	events := getObserverEvents()
	assert.True(t, hasEventType(events, bridgev1.AttachEventType_ATTACH_EVENT_TYPE_WRITER_RELEASED),
		"observer should see WRITER_RELEASED after voluntary release; got: %v", eventTypes(events))

	// Client B (observer): claim the now-vacant writer slot (force=false).
	claimResp, err := client.ClaimWriter(ctx, &bridgev1.ClaimWriterRequest{
		SessionId: sessionID,
		ClientId:  observerID,
		Force:     false,
	})
	require.NoError(t, err, "non-force claim should succeed when slot is vacant")
	assert.True(t, claimResp.Claimed)

	// Client B should now be able to write input.
	_, err = client.WriteInput(ctx, &bridgev1.WriteInputRequest{
		SessionId: sessionID,
		ClientId:  observerStream.ClientID(),
		Data:      []byte("OBSERVER_NOW_WRITER\n"),
	})
	require.NoError(t, err, "promoted observer should be able to write input")

	// Cleanup.
	_, _ = client.StopSession(ctx, &bridgev1.StopSessionRequest{
		SessionId: sessionID, Force: true,
	})
}

// eventTypes returns a slice of event type names for diagnostic output.
func eventTypes(events []*bridgev1.AttachSessionEvent) []string {
	names := make([]string, len(events))
	for i, ev := range events {
		names[i] = ev.Type.String()
	}
	return names
}

// --- Secure mode tests ---

// secureClient creates a bridgeclient connected to a secure-mode server
// using the auto-generated local-client credentials.
func secureClient(t *testing.T, target, stateDir string) *bridgeclient.Client {
	t.Helper()
	mat := localserver.LoadPKIMaterial(stateDir)
	client, err := bridgeclient.New(
		bridgeclient.WithTarget(target),
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: mat.CABundlePath,
			CertPath:     mat.LocalClientCert,
			KeyPath:      mat.LocalClientKey,
			ServerName:   "server",
		}),
		bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: mat.JWTSigningKey,
			Issuer:         "local",
			Audience:       "bridge",
		}),
	)
	require.NoError(t, err, "secure client should connect")
	return client
}

// TestSecureModeStartStop verifies that the server starts and stops
// cleanly in secure (mTLS+JWT) mode.
func TestSecureModeStartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("secure mode not supported on Windows")
	}

	stateDir := testStateDir(t)

	srv, err := localserver.Start(localserver.Config{
		StateDir:   stateDir,
		ListenAddr: "127.0.0.1:0",
		ServerSANs: []string{"127.0.0.1"},
	})
	require.NoError(t, err, "secure server should start")
	defer srv.Stop()

	// Verify mode file says "secure".
	mode := localserver.DiscoverMode(stateDir)
	assert.Equal(t, localserver.ModeSecure, mode)

	// Verify server is discoverable.
	target, discoveredMode := localserver.DiscoverTarget(stateDir)
	require.NotEmpty(t, target, "should discover running secure server")
	assert.Equal(t, localserver.ModeSecure, discoveredMode)

	// Health check via mTLS client.
	client := secureClient(t, target, stateDir)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := client.Health(ctx)
	require.NoError(t, err, "health check should succeed with mTLS")
	assert.NotEmpty(t, resp.ServerInstanceId)

	// Stop server.
	srv.Stop()

	// Verify server is no longer discoverable.
	assert.False(t, localserver.IsServerRunning(stateDir))
}

// TestSecureModeSessionLifecycle tests creating, listing, and stopping a
// session on a secure-mode server.
func TestSecureModeSessionLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("secure mode not supported on Windows")
	}

	stateDir := testStateDir(t)
	repoDir := t.TempDir()

	srv, err := localserver.Start(localserver.Config{
		StateDir:   stateDir,
		ListenAddr: "127.0.0.1:0",
		ServerSANs: []string{"127.0.0.1"},
	})
	require.NoError(t, err)
	defer srv.Stop()

	target, _ := localserver.DiscoverTarget(stateDir)
	client := secureClient(t, target, stateDir)
	defer func() { _ = client.Close() }()
	client.SetProject("test")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start a session.
	sessionID := uuid.NewString()
	startResp, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   "test",
		SessionId:   sessionID,
		RepoPath:    repoDir,
		Provider:    "echo",
		InitialCols: 80,
		InitialRows: 24,
	})
	require.NoError(t, err)
	assert.Equal(t, sessionID, startResp.SessionId)

	// Wait for session to start.
	var info *bridgev1.GetSessionResponse
	for i := 0; i < 20; i++ {
		info, err = client.GetSession(ctx, &bridgev1.GetSessionRequest{
			SessionId: sessionID,
		})
		if err == nil && info.Status != bridgev1.SessionStatus_SESSION_STATUS_STARTING {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NoError(t, err)

	// List sessions.
	listResp, err := client.ListSessions(ctx, &bridgev1.ListSessionsRequest{
		ProjectId: "test",
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(listResp.Sessions), 1)

	// Stop session.
	_, err = client.StopSession(ctx, &bridgev1.StopSessionRequest{
		SessionId: sessionID,
		Force:     true,
	})
	require.NoError(t, err)
}

// TestSecureModeRejectsInsecureClient verifies that an insecure client
// cannot connect to a secure server.
func TestSecureModeRejectsInsecureClient(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("secure mode not supported on Windows")
	}

	stateDir := testStateDir(t)

	srv, err := localserver.Start(localserver.Config{
		StateDir:   stateDir,
		ListenAddr: "127.0.0.1:0",
		ServerSANs: []string{"127.0.0.1"},
	})
	require.NoError(t, err)
	defer srv.Stop()

	target, _ := localserver.DiscoverTarget(stateDir)
	require.NotEmpty(t, target)

	// Try connecting without TLS — should fail.
	insecureClient, err := bridgeclient.New(bridgeclient.WithTarget(target))
	require.NoError(t, err, "dial should succeed (lazy connection)")
	defer func() { _ = insecureClient.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = insecureClient.Health(ctx)
	assert.Error(t, err, "insecure client should not be able to call secure server")
}

// TestSecureModeCleanup verifies that secure-mode state files (including
// server.mode) are cleaned up on stop.
func TestSecureModeCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("secure mode not supported on Windows")
	}

	stateDir := testStateDir(t)

	srv, err := localserver.Start(localserver.Config{
		StateDir:   stateDir,
		ListenAddr: "127.0.0.1:0",
	})
	require.NoError(t, err)

	// Mode file should exist.
	_, err = os.Stat(filepath.Join(stateDir, "server.mode"))
	assert.NoError(t, err, "mode file should exist while running")

	srv.Stop()

	// Mode file should be cleaned up.
	_, err = os.Stat(filepath.Join(stateDir, "server.mode"))
	assert.True(t, os.IsNotExist(err), "mode file should be removed after stop")
}
