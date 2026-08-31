package remote_stepca_e2e

import (
	"context"
	"flag"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bridgev1 "github.com/orchael/bridgectl/gen/bridge/v1"
	"github.com/orchael/bridgectl/internal/localserver"
	"github.com/orchael/bridgectl/internal/pki"
	"github.com/orchael/bridgectl/pkg/bridgeclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	serverAddr  = flag.String("server", "", "bridge server address (host:port)")
	clientState = flag.String("client-state", "", "client state directory")
	repoPath    = flag.String("repo", "/tmp/bridgectl", "path to test repo")
)

// connectRemote creates a bridgeclient connected to the remote server
// using auto-discovered credentials from the client state directory.
func connectRemote(t *testing.T) *bridgeclient.Client {
	t.Helper()

	certsDir := localserver.CertsDir(*clientState)

	caBundle := findFile(certsDir, "ca-bundle.crt")
	cert := findCert(certsDir)
	key := strings.TrimSuffix(cert, ".crt") + ".key"
	jwtKey := findFile(certsDir, "jwt-signing.key")

	require.FileExists(t, caBundle, "ca-bundle.crt")
	require.FileExists(t, cert, "client cert")
	require.FileExists(t, key, "client key")
	require.FileExists(t, jwtKey, "jwt-signing.key")

	c, err := pki.LoadCert(cert)
	require.NoError(t, err)
	issuer := c.Subject.CommonName

	serverName := *serverAddr
	if idx := strings.Index(serverName, ":"); idx > 0 {
		serverName = serverName[:idx]
	}

	client, err := bridgeclient.New(
		bridgeclient.WithTarget(*serverAddr),
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: caBundle,
			CertPath:     cert,
			KeyPath:      key,
			ServerName:   serverName,
		}),
		bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: jwtKey,
			Issuer:         issuer,
			Audience:       "bridge",
			TTL:            5 * time.Minute,
		}),
		bridgeclient.WithTimeout(30*time.Second),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestRemoteHealth verifies the client can reach the server.
func TestRemoteHealth(t *testing.T) {
	client := connectRemote(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Health(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.ServerInstanceId)
}

// TestRemoteListProviders verifies provider discovery over mTLS.
func TestRemoteListProviders(t *testing.T) {
	client := connectRemote(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.ListProviders(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Providers)

	names := make(map[string]bool)
	for _, p := range resp.Providers {
		names[p.Provider] = true
	}
	assert.True(t, names["echo"], "echo provider should exist")
}

// TestRemoteEchoSession creates an echo session, lists it, and watches output.
func TestRemoteEchoSession(t *testing.T) {
	client := connectRemote(t)
	client.SetProject("remote-e2e")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startResp, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId: "remote-e2e",
		Provider:  "echo",
		RepoPath:  *repoPath,
	})
	require.NoError(t, err)
	sessionID := startResp.SessionId
	t.Logf("started echo session: %s", sessionID)

	// List sessions — should see our session.
	listResp, err := client.ListSessions(ctx, &bridgev1.ListSessionsRequest{
		ProjectId: "remote-e2e",
	})
	require.NoError(t, err)
	found := false
	for _, s := range listResp.Sessions {
		if s.SessionId == sessionID {
			found = true
			break
		}
	}
	assert.True(t, found, "session should appear in list")

	// Watch: attach as observer and confirm we receive the ATTACHED event.
	watchCtx, watchCancel := context.WithTimeout(ctx, 10*time.Second)
	defer watchCancel()

	stream, err := client.AttachSession(watchCtx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  "remote-watcher",
		Role:      bridgev1.AttachRole_ATTACH_ROLE_OBSERVER,
	})
	require.NoError(t, err)

	var attached atomic.Bool
	_ = stream.RecvAll(watchCtx, func(event *bridgev1.AttachSessionEvent) error {
		if event.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ATTACHED {
			attached.Store(true)
			t.Log("attached as observer to echo session")
			return context.Canceled // stop after attach confirmation
		}
		return nil
	})
	assert.True(t, attached.Load(), "should have received ATTACHED event")

	// Stop the session.
	_, err = client.StopSession(ctx, &bridgev1.StopSessionRequest{
		SessionId: sessionID,
	})
	require.NoError(t, err)
}

// TestRemoteClaudeSession starts a Claude session if CLAUDE_CODE_OAUTH_TOKEN
// is set, lists it remotely, and attaches as observer to read output.
func TestRemoteClaudeSession(t *testing.T) {
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") == "" {
		t.Skip("CLAUDE_CODE_OAUTH_TOKEN not set")
	}

	client := connectRemote(t)
	client.SetProject("remote-e2e")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	startResp, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId: "remote-e2e",
		Provider:  "claude",
		RepoPath:  *repoPath,
	})
	require.NoError(t, err)
	sessionID := startResp.SessionId
	t.Logf("started claude session: %s", sessionID)
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_, _ = client.StopSession(stopCtx, &bridgev1.StopSessionRequest{SessionId: sessionID})
	}()

	// List sessions remotely.
	listResp, err := client.ListSessions(ctx, &bridgev1.ListSessionsRequest{
		ProjectId: "remote-e2e",
	})
	require.NoError(t, err)
	found := false
	for _, s := range listResp.Sessions {
		if s.SessionId == sessionID {
			found = true
			t.Logf("claude session found: status=%s provider=%s", s.Status, s.Provider)
			break
		}
	}
	require.True(t, found, "claude session should appear in remote list")

	// Watch: attach as observer and read at least one output event.
	watchCtx, watchCancel := context.WithTimeout(ctx, 30*time.Second)
	defer watchCancel()

	stream, err := client.AttachSession(watchCtx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  "remote-claude-watcher",
		Role:      bridgev1.AttachRole_ATTACH_ROLE_OBSERVER,
	})
	require.NoError(t, err)

	var gotOutput atomic.Bool
	_ = stream.RecvAll(watchCtx, func(event *bridgev1.AttachSessionEvent) error {
		if event.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_OUTPUT {
			t.Logf("received claude output (%d bytes)", len(event.Payload))
			gotOutput.Store(true)
			return context.Canceled
		}
		if event.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ATTACHED {
			t.Log("attached as observer to claude session")
		}
		return nil
	})
	assert.True(t, gotOutput.Load(), "should have received at least one output event from claude")
}

// TestRemoteCodexSession starts a Codex session if CODEX_AUTH is set,
// lists it remotely, and attaches as observer to read output.
func TestRemoteCodexSession(t *testing.T) {
	if os.Getenv("CODEX_AUTH") == "" {
		t.Skip("CODEX_AUTH not set")
	}

	client := connectRemote(t)
	client.SetProject("remote-e2e")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	startResp, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId: "remote-e2e",
		Provider:  "codex",
		RepoPath:  *repoPath,
	})
	require.NoError(t, err)
	sessionID := startResp.SessionId
	t.Logf("started codex session: %s", sessionID)
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_, _ = client.StopSession(stopCtx, &bridgev1.StopSessionRequest{SessionId: sessionID})
	}()

	// List sessions remotely.
	listResp, err := client.ListSessions(ctx, &bridgev1.ListSessionsRequest{
		ProjectId: "remote-e2e",
	})
	require.NoError(t, err)
	found := false
	for _, s := range listResp.Sessions {
		if s.SessionId == sessionID {
			found = true
			t.Logf("codex session found: status=%s provider=%s", s.Status, s.Provider)
			break
		}
	}
	require.True(t, found, "codex session should appear in remote list")

	// Watch as observer.
	watchCtx, watchCancel := context.WithTimeout(ctx, 30*time.Second)
	defer watchCancel()

	stream, err := client.AttachSession(watchCtx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  "remote-codex-watcher",
		Role:      bridgev1.AttachRole_ATTACH_ROLE_OBSERVER,
	})
	require.NoError(t, err)

	var gotOutput atomic.Bool
	_ = stream.RecvAll(watchCtx, func(event *bridgev1.AttachSessionEvent) error {
		if event.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_OUTPUT {
			t.Logf("received codex output (%d bytes)", len(event.Payload))
			gotOutput.Store(true)
			return context.Canceled
		}
		if event.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ATTACHED {
			t.Log("attached as observer to codex session")
		}
		return nil
	})
	assert.True(t, gotOutput.Load(), "should have received at least one output event from codex")
}

// --- helpers ---

func findFile(dir, name string) string {
	p := dir + "/" + name
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

func findCert(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".crt") {
			continue
		}
		if e.Name() == "ca-bundle.crt" || e.Name() == "ca.crt" || e.Name() == "step-ca-root.crt" {
			continue
		}
		return dir + "/" + e.Name()
	}
	return ""
}
