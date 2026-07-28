//go:build e2e

// Package stepca_e2e tests the full Step CA → bridge server → client enrollment
// → echo session lifecycle. It runs inside a Docker container alongside a real
// Step CA instance and the bridge server.
//
// The test exercises the workflow a user would follow from the step-ca README:
//  1. Bridge server starts with Step CA for server cert (Tier-2 PKI)
//  2. Client connects with mTLS using a dev-client cert
//  3. Client enrolls a JWT key via RegisterJWTKey (like `bridgectl client enroll`)
//  4. Client reconnects with mTLS + JWT
//  5. Client creates an echo session, writes input, reads output, stops session
package stepca_e2e

import (
	"context"
	"crypto/x509"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/internal/pki"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

var (
	bridgeTarget = flag.String("bridge.target", "bridge:9445", "bridge server address")
	caBundlePath = flag.String("bridge.cacert", "", "CA bundle for server verification")
	clientCert   = flag.String("bridge.cert", "", "client mTLS certificate")
	clientKey    = flag.String("bridge.key", "", "client mTLS private key")
	testTimeout  = flag.Duration("bridge.timeout", 120*time.Second, "overall test timeout")
)

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

// TestStepCAHealthCheck verifies that the bridge server is reachable and
// healthy using the dev-client mTLS credentials (no JWT yet).
func TestStepCAHealthCheck(t *testing.T) {
	client := mtlsOnlyClient(t)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := client.Health(ctx)
	require.NoError(t, err, "health check should succeed with mTLS-only client")
	assert.NotEmpty(t, resp.ServerInstanceId, "server should report an instance ID")

	// Echo provider should be available.
	found := false
	for _, p := range resp.Providers {
		if p.Provider == "echo" {
			found = true
			assert.True(t, p.Available, "echo provider should be available")
		}
	}
	assert.True(t, found, "echo provider should be listed in health response")
}

// TestEnrollAndAuthenticate exercises the full enrollment flow:
//  1. Connect with mTLS only (dev-client cert from entrypoint)
//  2. Generate a JWT keypair locally
//  3. Call RegisterJWTKey to enroll the public key
//  4. Reconnect with mTLS + JWT
//  5. Verify authenticated RPCs succeed
func TestEnrollAndAuthenticate(t *testing.T) {
	// Step 1: mTLS-only connection.
	mtlsClient := mtlsOnlyClient(t)
	defer func() { _ = mtlsClient.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Step 2: Generate JWT keypair.
	keyDir := t.TempDir()
	pubPath, privPath, err := pki.GenerateJWTKeypair(keyDir, "jwt-signing")
	require.NoError(t, err, "should generate JWT keypair")

	pubKey, err := pki.LoadEd25519PublicKey(pubPath)
	require.NoError(t, err, "should load public key")
	pubDER, err := x509.MarshalPKIXPublicKey(pubKey)
	require.NoError(t, err, "should marshal public key")

	// Step 3: Register the JWT key (mTLS-only, no JWT required).
	issuer := fmt.Sprintf("e2e-stepca-%s", uuid.NewString()[:8])
	resp, err := mtlsClient.RegisterJWTKey(ctx, &bridgev1.RegisterJWTKeyRequest{
		PublicKey: pubDER,
		Issuer:    issuer,
	})
	require.NoError(t, err, "RegisterJWTKey should succeed with mTLS-only auth")
	assert.Equal(t, issuer, resp.Issuer, "issuer should match request")

	// Step 4: Reconnect with mTLS + JWT.
	enrolledClient := jwtClient(t, privPath, issuer)
	defer func() { _ = enrolledClient.Close() }()

	// Step 5: Verify authenticated RPCs work.
	enrolledClient.SetProject("step-ca-e2e")

	healthResp, err := enrolledClient.Health(ctx)
	require.NoError(t, err, "Health should succeed with enrolled JWT credentials")
	assert.NotEmpty(t, healthResp.ServerInstanceId)

	listResp, err := enrolledClient.ListSessions(ctx, &bridgev1.ListSessionsRequest{
		ProjectId: "step-ca-e2e",
	})
	require.NoError(t, err, "ListSessions should succeed with enrolled JWT credentials")
	assert.NotNil(t, listResp, "should return a valid response")
}

// TestEchoSessionRoundTrip is the full end-to-end test:
//  1. Enroll a JWT key
//  2. Create an echo session
//  3. Attach to the session
//  4. Write input and verify echo output
//  5. Stop the session
func TestEchoSessionRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), *testTimeout)
	defer cancel()

	// Enroll first.
	mtlsClient := mtlsOnlyClient(t)

	keyDir := t.TempDir()
	pubPath, privPath, err := pki.GenerateJWTKeypair(keyDir, "jwt-signing")
	require.NoError(t, err)

	pubKey, err := pki.LoadEd25519PublicKey(pubPath)
	require.NoError(t, err)
	pubDER, err := x509.MarshalPKIXPublicKey(pubKey)
	require.NoError(t, err)

	issuer := fmt.Sprintf("e2e-echo-%s", uuid.NewString()[:8])
	_, err = mtlsClient.RegisterJWTKey(ctx, &bridgev1.RegisterJWTKeyRequest{
		PublicKey: pubDER,
		Issuer:    issuer,
	})
	require.NoError(t, err, "enrollment should succeed")
	_ = mtlsClient.Close()

	// Connect with JWT.
	client := jwtClient(t, privPath, issuer)
	defer func() { _ = client.Close() }()
	client.SetProject("step-ca-e2e")

	// Create echo session.
	sessionID := uuid.NewString()
	startResp, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   "step-ca-e2e",
		SessionId:   sessionID,
		RepoPath:    "/tmp",
		Provider:    "echo",
		InitialCols: 80,
		InitialRows: 24,
	})
	require.NoError(t, err, "StartSession should succeed")
	assert.Equal(t, sessionID, startResp.SessionId)

	// Wait for session to be running.
	var info *bridgev1.GetSessionResponse
	for i := 0; i < 30; i++ {
		info, err = client.GetSession(ctx, &bridgev1.GetSessionRequest{
			SessionId: sessionID,
		})
		if err == nil && info.Status == bridgev1.SessionStatus_SESSION_STATUS_RUNNING {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, err, "GetSession should succeed")
	require.Equal(t, bridgev1.SessionStatus_SESSION_STATUS_RUNNING, info.Status,
		"session should be running")

	// Verify session appears in list.
	listResp, err := client.ListSessions(ctx, &bridgev1.ListSessionsRequest{
		ProjectId: "step-ca-e2e",
	})
	require.NoError(t, err)
	found := false
	for _, s := range listResp.Sessions {
		if s.SessionId == sessionID {
			found = true
		}
	}
	require.True(t, found, "session should appear in list")

	// Attach to session.
	clientID := uuid.NewString()
	stream, err := client.AttachSession(ctx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  clientID,
		AfterSeq:  0,
	})
	require.NoError(t, err, "AttachSession should succeed")

	// Collect output in background.
	var received strings.Builder
	var mu sync.Mutex
	readCtx, readCancel := context.WithTimeout(ctx, 15*time.Second)
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
				if strings.Contains(got, "STEP_CA_E2E_ECHO") {
					readCancel()
				}
			}
			return nil
		})
	}()

	// Wait for attach.
	select {
	case <-attached:
	case <-time.After(5 * time.Second):
		t.Log("timeout waiting for attach event, trying write anyway")
	}

	// Write input — echo provider (cat) echoes it back.
	testMsg := "STEP_CA_E2E_ECHO\n"
	_, err = client.WriteInput(ctx, &bridgev1.WriteInputRequest{
		SessionId: sessionID,
		ClientId:  stream.ClientID(),
		Data:      []byte(testMsg),
	})
	require.NoError(t, err, "WriteInput should succeed")

	// Wait for output.
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}

	mu.Lock()
	got := received.String()
	mu.Unlock()
	require.NotEmpty(t, got, "echo provider should return output")
	assert.Contains(t, got, "STEP_CA_E2E_ECHO",
		"echo provider should echo back the input")

	// Stop session.
	_, err = client.StopSession(ctx, &bridgev1.StopSessionRequest{
		SessionId: sessionID,
		Force:     true,
	})
	require.NoError(t, err, "StopSession should succeed")

	// Verify session is stopped.
	time.Sleep(300 * time.Millisecond)
	info, err = client.GetSession(ctx, &bridgev1.GetSessionRequest{
		SessionId: sessionID,
	})
	require.NoError(t, err)
	assert.True(t,
		info.Status == bridgev1.SessionStatus_SESSION_STATUS_STOPPED ||
			info.Status == bridgev1.SessionStatus_SESSION_STATUS_FAILED,
		"session should be stopped or failed, got %v", info.Status)
}

// TestMultipleEnrollments verifies that multiple clients can enroll
// independently and each can authenticate with their own JWT key.
func TestMultipleEnrollments(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	type enrolledCreds struct {
		privPath string
		issuer   string
	}

	creds := make([]enrolledCreds, 3)
	for i := range creds {
		mtlsClient := mtlsOnlyClient(t)

		keyDir := t.TempDir()
		pubPath, privPath, err := pki.GenerateJWTKeypair(keyDir, "jwt-signing")
		require.NoError(t, err)

		pubKey, err := pki.LoadEd25519PublicKey(pubPath)
		require.NoError(t, err)
		pubDER, err := x509.MarshalPKIXPublicKey(pubKey)
		require.NoError(t, err)

		issuer := fmt.Sprintf("multi-%d-%s", i, uuid.NewString()[:8])
		_, err = mtlsClient.RegisterJWTKey(ctx, &bridgev1.RegisterJWTKeyRequest{
			PublicKey: pubDER,
			Issuer:    issuer,
		})
		require.NoError(t, err, "enrollment %d should succeed", i)
		_ = mtlsClient.Close()

		creds[i] = enrolledCreds{privPath: privPath, issuer: issuer}
	}

	// Each enrolled client should be able to authenticate independently.
	for i, c := range creds {
		client := jwtClient(t, c.privPath, c.issuer)
		client.SetProject("step-ca-e2e")

		resp, err := client.Health(ctx)
		require.NoError(t, err, "client %d should authenticate with its own JWT key", i)
		assert.NotEmpty(t, resp.ServerInstanceId)

		_ = client.Close()
	}
}

// --- helpers ---

// mtlsOnlyClient creates a client using the dev-client mTLS cert (no JWT).
func mtlsOnlyClient(t *testing.T) *bridgeclient.Client {
	t.Helper()
	client, err := bridgeclient.New(
		bridgeclient.WithTarget(*bridgeTarget),
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: *caBundlePath,
			CertPath:     *clientCert,
			KeyPath:      *clientKey,
			ServerName:   "server",
		}),
		bridgeclient.WithTimeout(15*time.Second),
	)
	require.NoError(t, err, "mTLS client should connect")
	return client
}

// jwtClient creates a client using mTLS + JWT auth.
func jwtClient(t *testing.T, privKeyPath, issuer string) *bridgeclient.Client {
	t.Helper()
	client, err := bridgeclient.New(
		bridgeclient.WithTarget(*bridgeTarget),
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: *caBundlePath,
			CertPath:     *clientCert,
			KeyPath:      *clientKey,
			ServerName:   "server",
		}),
		bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: privKeyPath,
			Issuer:         issuer,
			Audience:       "bridge",
		}),
		bridgeclient.WithTimeout(15*time.Second),
	)
	require.NoError(t, err, "JWT client should connect")
	return client
}
