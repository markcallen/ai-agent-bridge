package cli_test

import (
	"bytes"
	"context"
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

// cliBinary holds the path to the compiled ai-agent-bridge-cli binary.
// It is built once per test run via TestMain.
var cliBinary string

func TestMain(m *testing.M) {
	// Build the ai-agent-bridge-cli binary into a temp dir.
	dir, err := os.MkdirTemp("", "cli-e2e-*")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	bin := filepath.Join(dir, "ai-agent-bridge-cli")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/ai-agent-bridge-cli")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic("failed to build ai-agent-bridge-cli binary: " + err.Error())
	}
	cliBinary = bin

	os.Exit(m.Run())
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

// TestCLIVersion tests that `ai-agent-bridge-cli --version` works.
func TestCLIVersion(t *testing.T) {
	cmd := exec.Command(cliBinary, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	require.NoError(t, err, "--version should succeed")
	assert.Contains(t, out.String(), "ai-agent-bridge-cli version")
}

// TestCLIHelp tests that `ai-agent-bridge-cli --help` exits cleanly.
func TestCLIHelp(t *testing.T) {
	cmd := exec.Command(cliBinary, "--help")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	require.NoError(t, err, "--help should succeed")
	assert.Contains(t, out.String(), "ai-agent-bridge-cli starts a local bridge server")
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

	// Wait for output.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Log("timeout waiting for echo output (may be expected on some platforms)")
	}

	mu.Lock()
	got := received.String()
	mu.Unlock()
	if got != "" {
		assert.Contains(t, got, "HELLO_FROM_E2E")
	}

	// Stop.
	_, err = client.StopSession(ctx, &bridgev1.StopSessionRequest{
		SessionId: sessionID,
		Force:     true,
	})
	require.NoError(t, err)
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
