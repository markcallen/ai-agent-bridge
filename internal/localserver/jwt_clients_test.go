package localserver

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/markcallen/ai-agent-bridge/internal/auth"
	"github.com/markcallen/ai-agent-bridge/internal/pki"
	"github.com/stretchr/testify/require"
)

func writeTestJWTPubKey(t *testing.T, path string) ed25519.PublicKey {
	t.Helper()
	pubPath, _, err := pki.GenerateJWTKeypair(filepath.Dir(path), "generated")
	require.NoError(t, err)
	data, err := os.ReadFile(pubPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))
	pub, err := pki.LoadEd25519PublicKey(path)
	require.NoError(t, err)
	return pub
}

func TestLoadConfiguredJWTClients(t *testing.T) {
	stateDir := t.TempDir()
	keyPath := filepath.Join(stateDir, "custom", "do-dev2.pub")
	require.NoError(t, os.MkdirAll(filepath.Dir(keyPath), 0o700))
	writeTestJWTPubKey(t, keyPath)

	verifier := &auth.JWTVerifier{
		Keys:     map[string]ed25519.PublicKey{},
		Audience: "bridge",
		MaxTTL:   10 * time.Minute,
	}

	err := loadConfiguredJWTClients(verifier, stateDir, testLogger(), []ConfiguredJWTClient{
		{Issuer: "do-dev2", KeyPath: keyPath},
		{Issuer: "missing-optional"},
	})
	require.NoError(t, err)
	require.True(t, verifier.HasKey("do-dev2"))
	require.False(t, verifier.HasKey("missing-optional"))
}

func TestLoadConfiguredJWTClientsDefaultPath(t *testing.T) {
	stateDir := t.TempDir()
	defaultPath := filepath.Join(CertsDir(stateDir), "jwt-clients", "laptop-a.pub")
	require.NoError(t, os.MkdirAll(filepath.Dir(defaultPath), 0o700))
	writeTestJWTPubKey(t, defaultPath)

	verifier := &auth.JWTVerifier{
		Keys:     map[string]ed25519.PublicKey{},
		Audience: "bridge",
		MaxTTL:   10 * time.Minute,
	}

	err := loadConfiguredJWTClients(verifier, stateDir, testLogger(), []ConfiguredJWTClient{
		{Issuer: "laptop-a"},
	})
	require.NoError(t, err)
	require.True(t, verifier.HasKey("laptop-a"))
}

func TestLoadConfiguredJWTClientsRequiredMissing(t *testing.T) {
	stateDir := t.TempDir()
	verifier := &auth.JWTVerifier{
		Keys:     map[string]ed25519.PublicKey{},
		Audience: "bridge",
		MaxTTL:   10 * time.Minute,
	}

	err := loadConfiguredJWTClients(verifier, stateDir, testLogger(), []ConfiguredJWTClient{
		{Issuer: "required-client", Required: true},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "required-client")
}

func TestLoadConfiguredJWTClientsOptionalInvalid(t *testing.T) {
	stateDir := t.TempDir()
	keyPath := filepath.Join(stateDir, "invalid.pub")
	require.NoError(t, os.WriteFile(keyPath, []byte("not a public key"), 0o644))

	verifier := &auth.JWTVerifier{
		Keys:     map[string]ed25519.PublicKey{},
		Audience: "bridge",
		MaxTTL:   10 * time.Minute,
	}

	err := loadConfiguredJWTClients(verifier, stateDir, testLogger(), []ConfiguredJWTClient{
		{Issuer: "invalid-optional", KeyPath: keyPath},
	})
	require.NoError(t, err)
	require.False(t, verifier.HasKey("invalid-optional"))
}

func TestBuildSecureGRPCOptsLoadsConfiguredJWTClients(t *testing.T) {
	stateDir := t.TempDir()
	logger := testLogger()
	mat, err := EnsurePKI(stateDir, []string{"server", "127.0.0.1"}, logger, nil, 0)
	require.NoError(t, err)

	keyPath := filepath.Join(stateDir, "configured.pub")
	writeTestJWTPubKey(t, keyPath)

	_, verifier, err := buildSecureGRPCOpts(mat, stateDir, logger, nil, []ConfiguredJWTClient{
		{Issuer: "configured-client", KeyPath: keyPath},
	})
	require.NoError(t, err)
	require.True(t, verifier.HasKey("configured-client"))
}
