//go:build e2e

package remote_mtls_e2e

import (
	"context"
	"flag"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	bridgev1 "github.com/orchael/bridgectl/gen/bridge/v1"
	"github.com/orchael/bridgectl/internal/localserver"
	"github.com/orchael/bridgectl/internal/pki"
	"github.com/orchael/bridgectl/pkg/bridgeclient"
	"github.com/stretchr/testify/suite"
)

var (
	serverAddr  = flag.String("server", "", "bridge server address (host:port)")
	clientState = flag.String("client-state", "", "client state directory")
	repoPath    = flag.String("repo", "/tmp/bridgectl", "path to test repo")
)

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

// RemoteMTLSSuite tests remote mTLS connectivity, provider listing, and session lifecycle.
type RemoteMTLSSuite struct {
	suite.Suite
}

func TestRemoteMTLSSuite(t *testing.T) {
	suite.Run(t, new(RemoteMTLSSuite))
}

// connectRemote creates a bridgeclient connected to the remote server
// using auto-discovered credentials from the client state directory.
func (s *RemoteMTLSSuite) connectRemote() *bridgeclient.Client {
	t := s.T()
	t.Helper()

	certsDir := localserver.CertsDir(*clientState)

	caBundle := findFile(certsDir, "ca-bundle.crt")
	cert := findCert(certsDir)
	key := strings.TrimSuffix(cert, ".crt") + ".key"
	jwtKey := findFile(certsDir, "jwt-signing.key")

	s.Require().FileExists(caBundle, "ca-bundle.crt")
	s.Require().FileExists(cert, "client cert")
	s.Require().FileExists(key, "client key")
	s.Require().FileExists(jwtKey, "jwt-signing.key")

	c, err := pki.LoadCert(cert)
	s.Require().NoError(err)
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
	s.Require().NoError(err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestRemoteHealth verifies the client can reach the server.
func (s *RemoteMTLSSuite) TestRemoteHealth() {
	client := s.connectRemote()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Health(ctx)
	s.Require().NoError(err)
	s.Assert().NotEmpty(resp.ServerInstanceId)
}

// TestRemoteListProviders verifies provider discovery over mTLS.
func (s *RemoteMTLSSuite) TestRemoteListProviders() {
	client := s.connectRemote()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.ListProviders(ctx)
	s.Require().NoError(err)
	s.Assert().NotEmpty(resp.Providers)

	names := make(map[string]bool)
	for _, p := range resp.Providers {
		names[p.Provider] = true
	}
	s.Assert().True(names["echo"], "echo provider should exist")
}

// TestRemoteEchoSession creates an echo session, lists it, and watches output.
func (s *RemoteMTLSSuite) TestRemoteEchoSession() {
	client := s.connectRemote()
	client.SetProject("remote-e2e")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startResp, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   "remote-e2e",
		SessionId:   uuid.NewString(),
		Provider:    "echo",
		RepoPath:    *repoPath,
		InitialCols: 80,
		InitialRows: 24,
	})
	s.Require().NoError(err)
	sessionID := startResp.SessionId
	s.T().Logf("started echo session: %s", sessionID)

	// List sessions — should see our session.
	listResp, err := client.ListSessions(ctx, &bridgev1.ListSessionsRequest{
		ProjectId: "remote-e2e",
	})
	s.Require().NoError(err)
	found := false
	for _, sess := range listResp.Sessions {
		if sess.SessionId == sessionID {
			found = true
			break
		}
	}
	s.Assert().True(found, "session should appear in list")

	// Watch: attach as observer and confirm we receive the ATTACHED event.
	watchCtx, watchCancel := context.WithTimeout(ctx, 10*time.Second)
	defer watchCancel()

	stream, err := client.AttachSession(watchCtx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  "remote-watcher",
		Role:      bridgev1.AttachRole_ATTACH_ROLE_OBSERVER,
	})
	s.Require().NoError(err)

	var attached atomic.Bool
	_ = stream.RecvAll(watchCtx, func(event *bridgev1.AttachSessionEvent) error {
		if event.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ATTACHED {
			attached.Store(true)
			s.T().Log("attached as observer to echo session")
			return context.Canceled // stop after attach confirmation
		}
		return nil
	})
	s.Assert().True(attached.Load(), "should have received ATTACHED event")

	// Stop the session.
	_, err = client.StopSession(ctx, &bridgev1.StopSessionRequest{
		SessionId: sessionID,
	})
	s.Require().NoError(err)
}

// TestRemoteClaudeSession starts a Claude session if CLAUDE_CODE_OAUTH_TOKEN
// is set, lists it remotely, and attaches as observer to read output.
func (s *RemoteMTLSSuite) TestRemoteClaudeSession() {
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") == "" {
		s.T().Skip("CLAUDE_CODE_OAUTH_TOKEN not set")
	}

	client := s.connectRemote()
	client.SetProject("remote-e2e")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	startResp, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   "remote-e2e",
		SessionId:   uuid.NewString(),
		Provider:    "claude",
		RepoPath:    *repoPath,
		InitialCols: 80,
		InitialRows: 24,
	})
	s.Require().NoError(err)
	sessionID := startResp.SessionId
	s.T().Logf("started claude session: %s", sessionID)
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_, _ = client.StopSession(stopCtx, &bridgev1.StopSessionRequest{SessionId: sessionID})
	}()

	// List sessions remotely.
	listResp, err := client.ListSessions(ctx, &bridgev1.ListSessionsRequest{
		ProjectId: "remote-e2e",
	})
	s.Require().NoError(err)
	found := false
	for _, sess := range listResp.Sessions {
		if sess.SessionId == sessionID {
			found = true
			s.T().Logf("claude session found: status=%s provider=%s", sess.Status, sess.Provider)
			break
		}
	}
	s.Require().True(found, "claude session should appear in remote list")

	// Watch: attach as observer and read at least one output event.
	watchCtx, watchCancel := context.WithTimeout(ctx, 30*time.Second)
	defer watchCancel()

	stream, err := client.AttachSession(watchCtx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  "remote-claude-watcher",
		Role:      bridgev1.AttachRole_ATTACH_ROLE_OBSERVER,
	})
	s.Require().NoError(err)

	var gotOutput atomic.Bool
	_ = stream.RecvAll(watchCtx, func(event *bridgev1.AttachSessionEvent) error {
		if event.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_OUTPUT {
			s.T().Logf("received claude output (%d bytes)", len(event.Payload))
			gotOutput.Store(true)
			return context.Canceled
		}
		if event.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ATTACHED {
			s.T().Log("attached as observer to claude session")
		}
		return nil
	})
	s.Assert().True(gotOutput.Load(), "should have received at least one output event from claude")
}

// TestRemoteCodexSession starts a Codex session if CODEX_AUTH is set,
// lists it remotely, and attaches as observer to read output.
func (s *RemoteMTLSSuite) TestRemoteCodexSession() {
	if os.Getenv("CODEX_AUTH") == "" {
		s.T().Skip("CODEX_AUTH not set")
	}

	client := s.connectRemote()
	client.SetProject("remote-e2e")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	startResp, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   "remote-e2e",
		SessionId:   uuid.NewString(),
		Provider:    "codex",
		RepoPath:    *repoPath,
		InitialCols: 80,
		InitialRows: 24,
	})
	s.Require().NoError(err)
	sessionID := startResp.SessionId
	s.T().Logf("started codex session: %s", sessionID)
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_, _ = client.StopSession(stopCtx, &bridgev1.StopSessionRequest{SessionId: sessionID})
	}()

	// List sessions remotely.
	listResp, err := client.ListSessions(ctx, &bridgev1.ListSessionsRequest{
		ProjectId: "remote-e2e",
	})
	s.Require().NoError(err)
	found := false
	for _, sess := range listResp.Sessions {
		if sess.SessionId == sessionID {
			found = true
			s.T().Logf("codex session found: status=%s provider=%s", sess.Status, sess.Provider)
			break
		}
	}
	s.Require().True(found, "codex session should appear in remote list")

	// Watch as observer.
	watchCtx, watchCancel := context.WithTimeout(ctx, 30*time.Second)
	defer watchCancel()

	stream, err := client.AttachSession(watchCtx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  "remote-codex-watcher",
		Role:      bridgev1.AttachRole_ATTACH_ROLE_OBSERVER,
	})
	s.Require().NoError(err)

	var gotOutput atomic.Bool
	_ = stream.RecvAll(watchCtx, func(event *bridgev1.AttachSessionEvent) error {
		if event.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_OUTPUT {
			s.T().Logf("received codex output (%d bytes)", len(event.Payload))
			gotOutput.Store(true)
			return context.Canceled
		}
		if event.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ATTACHED {
			s.T().Log("attached as observer to codex session")
		}
		return nil
	})
	s.Assert().True(gotOutput.Load(), "should have received at least one output event from codex")
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
