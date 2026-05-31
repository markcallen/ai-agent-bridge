package localserver

import (
	"crypto/x509"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/markcallen/ai-agent-bridge/internal/pki"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestEnsurePKI(t *testing.T) {
	stateDir := t.TempDir()
	sans := []string{"10.0.0.1", "bridge.local"}

	mat, err := EnsurePKI(stateDir, sans, testLogger())
	require.NoError(t, err)

	// Verify all files exist.
	for _, path := range []string{
		mat.CACertPath,
		mat.CAKeyPath,
		mat.ServerCertPath,
		mat.ServerKeyPath,
		mat.LocalClientCert,
		mat.LocalClientKey,
		mat.CABundlePath,
		mat.JWTSigningKey,
		mat.JWTSigningPub,
	} {
		_, err := os.Stat(path)
		assert.NoError(t, err, "file should exist: %s", path)
	}

	// Verify private keys have restricted permissions.
	for _, path := range []string{mat.CAKeyPath, mat.ServerKeyPath, mat.LocalClientKey, mat.JWTSigningKey} {
		info, err := os.Stat(path)
		require.NoError(t, err)
		perm := info.Mode().Perm()
		assert.Equal(t, os.FileMode(0o600), perm, "private key %s should be 0600", path)
	}

	// Verify server cert has the expected SANs.
	serverCert, err := pki.LoadCert(mat.ServerCertPath)
	require.NoError(t, err)
	assert.Contains(t, serverCert.DNSNames, "bridge.local")
	foundIP := false
	for _, ip := range serverCert.IPAddresses {
		if ip.String() == "10.0.0.1" {
			foundIP = true
		}
	}
	assert.True(t, foundIP, "server cert should have IP SAN 10.0.0.1")

	// Verify server cert is signed by the CA.
	caCert, _, err := pki.LoadCA(mat.CACertPath, mat.CAKeyPath)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	_, err = serverCert.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	assert.NoError(t, err, "server cert should verify against CA")

	// Verify local-client cert is signed by the CA.
	clientCert, err := pki.LoadCert(mat.LocalClientCert)
	require.NoError(t, err)
	_, err = clientCert.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	assert.NoError(t, err, "client cert should verify against CA")

	// Verify JWT keypair loads successfully.
	_, err = pki.LoadEd25519PublicKey(mat.JWTSigningPub)
	assert.NoError(t, err, "JWT public key should load")
	_, err = pki.LoadEd25519PrivateKey(mat.JWTSigningKey)
	assert.NoError(t, err, "JWT private key should load")
}

func TestEnsurePKI_Idempotent(t *testing.T) {
	stateDir := t.TempDir()
	sans := []string{"10.0.0.1"}

	mat1, err := EnsurePKI(stateDir, sans, testLogger())
	require.NoError(t, err)

	// Read the CA cert bytes from first run.
	ca1, err := os.ReadFile(mat1.CACertPath)
	require.NoError(t, err)

	// Second call should be a no-op.
	mat2, err := EnsurePKI(stateDir, sans, testLogger())
	require.NoError(t, err)

	// CA cert should be identical (not regenerated).
	ca2, err := os.ReadFile(mat2.CACertPath)
	require.NoError(t, err)
	assert.Equal(t, ca1, ca2, "CA cert should not be regenerated on second call")
}

func TestIssueClientCert(t *testing.T) {
	stateDir := t.TempDir()
	logger := testLogger()

	// First generate the CA via EnsurePKI.
	mat, err := EnsurePKI(stateDir, []string{"127.0.0.1"}, logger)
	require.NoError(t, err)

	// Issue a client cert.
	certPath, keyPath, err := IssueClientCert(stateDir, "remote-dev", logger)
	require.NoError(t, err)

	// Verify files exist in the expected location.
	expectedDir := filepath.Join(CertsDir(stateDir), "clients", "remote-dev")
	assert.Equal(t, filepath.Join(expectedDir, "remote-dev.crt"), certPath)
	assert.Equal(t, filepath.Join(expectedDir, "remote-dev.key"), keyPath)

	_, err = os.Stat(certPath)
	assert.NoError(t, err)
	_, err = os.Stat(keyPath)
	assert.NoError(t, err)

	// Verify the cert validates against the CA.
	caCert, _, err := pki.LoadCA(mat.CACertPath, mat.CAKeyPath)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	clientCert, err := pki.LoadCert(certPath)
	require.NoError(t, err)
	_, err = clientCert.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	assert.NoError(t, err, "issued client cert should verify against CA")
	assert.Equal(t, "remote-dev", clientCert.Subject.CommonName)

	// Verify per-client JWT keypair was created.
	clientJWTKey := filepath.Join(expectedDir, "jwt-signing.key")
	clientJWTPub := filepath.Join(expectedDir, "jwt-signing.pub")
	_, err = os.Stat(clientJWTKey)
	assert.NoError(t, err, "per-client JWT key should exist")
	_, err = os.Stat(clientJWTPub)
	assert.NoError(t, err, "per-client JWT pub should exist")

	// Verify server-side copy of the public key.
	serverPubCopy := filepath.Join(CertsDir(stateDir), "jwt-clients", "remote-dev.pub")
	_, err = os.Stat(serverPubCopy)
	assert.NoError(t, err, "server-side JWT pub copy should exist")
}

func TestIssueClientCert_RejectsPathTraversal(t *testing.T) {
	stateDir := t.TempDir()
	logger := testLogger()

	// Generate PKI first.
	_, err := EnsurePKI(stateDir, []string{"127.0.0.1"}, logger)
	require.NoError(t, err)

	badNames := []string{"../escape", "foo/bar", ".hidden", "", "a b c"}
	for _, name := range badNames {
		_, _, err := IssueClientCert(stateDir, name, logger)
		assert.Error(t, err, "should reject client name %q", name)
	}

	// Valid names should work.
	goodNames := []string{"laptop2", "dev-machine", "server.local", "test_01"}
	for _, name := range goodNames {
		_, _, err := IssueClientCert(stateDir, name, logger)
		assert.NoError(t, err, "should accept client name %q", name)
	}
}

func TestLoadPKIMaterial(t *testing.T) {
	stateDir := "/tmp/test-state"
	mat := LoadPKIMaterial(stateDir)

	assert.Equal(t, "/tmp/test-state/certs/ca.crt", mat.CACertPath)
	assert.Equal(t, "/tmp/test-state/certs/server.crt", mat.ServerCertPath)
	assert.Equal(t, "/tmp/test-state/certs/local-client.crt", mat.LocalClientCert)
	assert.Equal(t, "/tmp/test-state/certs/jwt-signing.key", mat.JWTSigningKey)
}

func TestBuildServerSANs(t *testing.T) {
	tests := []struct {
		name       string
		listenAddr string
		extra      []string
		wantHas    []string
		wantNot    []string
	}{
		{
			name:       "extracts host from addr",
			listenAddr: "10.0.0.1:9445",
			extra:      nil,
			wantHas:    []string{"10.0.0.1", "127.0.0.1", "localhost"},
		},
		{
			name:       "skips wildcard 0.0.0.0",
			listenAddr: "0.0.0.0:9445",
			extra:      []string{"vpn.example.com"},
			wantHas:    []string{"127.0.0.1", "localhost", "vpn.example.com"},
			wantNot:    []string{"0.0.0.0"},
		},
		{
			name:       "deduplicates",
			listenAddr: "10.0.0.1:9445",
			extra:      []string{"10.0.0.1", "extra.local"},
			wantHas:    []string{"10.0.0.1", "extra.local", "127.0.0.1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := buildServerSANs(tc.listenAddr, tc.extra)
			for _, want := range tc.wantHas {
				assert.Contains(t, result, want)
			}
			for _, not := range tc.wantNot {
				assert.NotContains(t, result, not)
			}
		})
	}
}
