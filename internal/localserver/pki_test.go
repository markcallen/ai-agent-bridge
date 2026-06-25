package localserver

import (
	"crypto/x509"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/markcallen/ai-agent-bridge/internal/pki"
)

// fakeStepDir writes a stub `step` binary into a temp directory and returns
// a cleanup function. The fake step writes empty placeholder files to the cert
// and key path arguments (positions 3 and 4 of `step ca certificate ...`).
func fakeStepDir(t *testing.T) (dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake step script not supported on Windows")
	}
	dir = t.TempDir()
	script := `#!/bin/sh
# Fake step CLI: write placeholder cert/key for "step ca certificate <name> <cert> <key> ..."
if [ "$1" = "ca" ] && [ "$2" = "certificate" ]; then
  echo "fake-cert" > "$4"
  echo "fake-key"  > "$5"
fi
exit 0
`
	scriptPath := filepath.Join(dir, "step")
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return dir
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestEnsurePKI(t *testing.T) {
	stateDir := t.TempDir()
	sans := []string{"10.0.0.1", "bridge.local"}

	mat, err := EnsurePKI(stateDir, sans, testLogger(), nil)
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

	mat1, err := EnsurePKI(stateDir, sans, testLogger(), nil)
	require.NoError(t, err)

	// Read the CA cert bytes from first run.
	ca1, err := os.ReadFile(mat1.CACertPath)
	require.NoError(t, err)

	// Second call should be a no-op.
	mat2, err := EnsurePKI(stateDir, sans, testLogger(), nil)
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
	mat, err := EnsurePKI(stateDir, []string{"127.0.0.1"}, logger, nil)
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
	_, err := EnsurePKI(stateDir, []string{"127.0.0.1"}, logger, nil)
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

// TestEnsurePKI_StepCASkipsAutoGen verifies that when a StepCAConfig is
// supplied but the `step` binary is absent, EnsurePKI does not generate a
// self-signed CA and instead returns a clear error.
func TestEnsurePKI_StepCASkipsAutoGen(t *testing.T) {
	stateDir := t.TempDir()
	// Write a fake root cert so the RootPath check passes.
	rootPEM := filepath.Join(stateDir, "root.crt")
	require.NoError(t, os.WriteFile(rootPEM, []byte("placeholder"), 0o644))

	stepCfg := &StepCAConfig{
		URL:      "https://ca.example.internal:443",
		RootPath: rootPEM,
	}
	_, err := EnsurePKI(stateDir, []string{"127.0.0.1"}, testLogger(), stepCfg)
	// `step` is not installed in the test environment, so we expect an error.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Step CA")

	// The auto-generated CA must NOT have been created.
	mat := LoadPKIMaterial(stateDir)
	_, statErr := os.Stat(mat.CAKeyPath)
	assert.True(t, os.IsNotExist(statErr), "CA key should not be auto-generated in Step CA mode")
}

// TestEnsurePKI_StepCAMissingRoot verifies that StepCAConfig without RootPath
// returns an error before calling the `step` binary.
func TestEnsurePKI_StepCAMissingRoot(t *testing.T) {
	stateDir := t.TempDir()
	stepCfg := &StepCAConfig{URL: "https://ca.example.internal:443"}
	_, err := EnsurePKI(stateDir, []string{"127.0.0.1"}, testLogger(), stepCfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step-ca-root")
}

// TestEnsurePKI_StepCAHappyPath exercises the Step CA code path using a stub
// `step` binary so the happy path is covered without a real Step CA server.
func TestEnsurePKI_StepCAHappyPath(t *testing.T) {
	fakeStepDir(t)
	stateDir := t.TempDir()
	rootPEM := filepath.Join(stateDir, "root.crt")
	require.NoError(t, os.WriteFile(rootPEM, []byte("fake-root-cert"), 0o644))

	stepCfg := &StepCAConfig{
		URL:      "https://ca.example.internal:443",
		RootPath: rootPEM,
	}
	mat, err := EnsurePKI(stateDir, []string{"10.0.0.1"}, testLogger(), stepCfg)
	require.NoError(t, err)

	// ca-bundle.crt should start with the Step CA root, followed by the local CA.
	bundle, err := os.ReadFile(mat.CABundlePath)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(bundle), "fake-root-cert"), "bundle should start with Step CA root")
	assert.Contains(t, string(bundle), "BEGIN CERTIFICATE", "bundle should also contain local CA cert")

	// Server cert and key files should exist (written by the fake step script).
	_, err = os.Stat(mat.ServerCertPath)
	assert.NoError(t, err, "server cert should exist")
	_, err = os.Stat(mat.ServerKeyPath)
	assert.NoError(t, err, "server key should exist")

	// Local-client cert and key must exist so CLI probing works in Step CA mode.
	_, err = os.Stat(mat.LocalClientCert)
	assert.NoError(t, err, "local-client cert should exist")
	_, err = os.Stat(mat.LocalClientKey)
	assert.NoError(t, err, "local-client key should exist")

	// JWT keypair should be auto-generated locally even in Step CA mode.
	_, err = os.Stat(mat.JWTSigningPub)
	assert.NoError(t, err, "JWT pub should exist")
	_, err = os.Stat(mat.JWTSigningKey)
	assert.NoError(t, err, "JWT key should exist")
}

// TestEnsurePKI_StepCAIdempotent verifies that a second EnsurePKI call with
// Step CA config is a no-op when ca-bundle.crt already exists.
func TestEnsurePKI_StepCAIdempotent(t *testing.T) {
	fakeStepDir(t)
	stateDir := t.TempDir()
	rootPEM := filepath.Join(stateDir, "root.crt")
	require.NoError(t, os.WriteFile(rootPEM, []byte("fake-root-cert"), 0o644))

	stepCfg := &StepCAConfig{URL: "https://ca.example.internal:443", RootPath: rootPEM}
	_, err := EnsurePKI(stateDir, []string{"10.0.0.1"}, testLogger(), stepCfg)
	require.NoError(t, err)

	// Overwrite root file with different content.
	require.NoError(t, os.WriteFile(rootPEM, []byte("changed-root"), 0o644))
	// Second call should be no-op: bundle should still have the original content.
	mat2, err := EnsurePKI(stateDir, []string{"10.0.0.1"}, testLogger(), stepCfg)
	require.NoError(t, err)
	bundle, _ := os.ReadFile(mat2.CABundlePath)
	assert.True(t, strings.HasPrefix(string(bundle), "fake-root-cert"), "bundle should start with original Step CA root, not overwritten")
	assert.NotContains(t, string(bundle), "changed-root", "bundle should not reflect the overwritten root file")
}

// TestIssueClientCertViaOIDC_HappyPath exercises the full OIDC enrollment path
// using a stub `step` binary.
func TestIssueClientCertViaOIDC_HappyPath(t *testing.T) {
	fakeStepDir(t)
	stateDir := t.TempDir()
	rootPEM := filepath.Join(stateDir, "root.crt")
	require.NoError(t, os.WriteFile(rootPEM, []byte("fake-root"), 0o644))

	stepCfg := &StepCAConfig{
		URL:             "https://ca.example.internal:443",
		RootPath:        rootPEM,
		OIDCProviderURL: "https://accounts.google.com",
	}
	logger := testLogger()
	certPath, keyPath, err := IssueClientCertViaOIDC(stateDir, "mark", stepCfg, logger)
	require.NoError(t, err)

	expectedDir := filepath.Join(CertsDir(stateDir), "clients", "mark")
	assert.Equal(t, filepath.Join(expectedDir, "mark.crt"), certPath)
	assert.Equal(t, filepath.Join(expectedDir, "mark.key"), keyPath)

	_, err = os.Stat(certPath)
	assert.NoError(t, err, "cert file should exist")
	_, err = os.Stat(keyPath)
	assert.NoError(t, err, "key file should exist")

	// JWT keypair should be generated locally.
	_, err = os.Stat(filepath.Join(expectedDir, "jwt-signing.key"))
	assert.NoError(t, err, "client JWT key should exist")
	// Server-side copy of the JWT public key.
	serverPub := filepath.Join(CertsDir(stateDir), "jwt-clients", "mark.pub")
	_, err = os.Stat(serverPub)
	assert.NoError(t, err, "server-side JWT pub copy should exist")
}

// TestCopyFile_ErrorOnMissingSource verifies that copyFile returns an error when
// the source file does not exist.
func TestCopyFile_ErrorOnMissingSource(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "dst.txt")
	err := copyFile("/nonexistent/source.txt", dst)
	require.Error(t, err)
}

// TestCopyFile_ErrorOnUnwritableDest verifies that copyFile returns an error
// when the destination directory does not exist.
func TestCopyFile_ErrorOnUnwritableDest(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.txt")
	require.NoError(t, os.WriteFile(src, []byte("data"), 0o644))
	err := copyFile(src, "/nonexistent/dir/dst.txt")
	require.Error(t, err)
}

// TestIssueClientCertViaOIDC_RequiresStepBinary verifies that
// IssueClientCertViaOIDC returns a useful error when the `step` CLI is absent.
func TestIssueClientCertViaOIDC_RequiresStepBinary(t *testing.T) {
	stateDir := t.TempDir()
	logger := testLogger()
	rootPEM := filepath.Join(stateDir, "root.crt")
	require.NoError(t, os.WriteFile(rootPEM, []byte("placeholder"), 0o644))

	stepCfg := &StepCAConfig{
		URL:             "https://ca.example.internal:443",
		RootPath:        rootPEM,
		OIDCProviderURL: "https://accounts.google.com",
	}
	_, _, err := IssueClientCertViaOIDC(stateDir, "mark", stepCfg, logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step")
}

// TestIssueClientCertViaOIDC_Validation verifies that IssueClientCertViaOIDC
// validates its inputs before trying to run `step`.
func TestIssueClientCertViaOIDC_Validation(t *testing.T) {
	stateDir := t.TempDir()
	logger := testLogger()
	rootPEM := filepath.Join(stateDir, "root.crt")
	require.NoError(t, os.WriteFile(rootPEM, []byte("placeholder"), 0o644))

	tests := []struct {
		name    string
		client  string
		stepCA  *StepCAConfig
		wantErr string
	}{
		{
			name:    "invalid client name",
			client:  "../escape",
			stepCA:  &StepCAConfig{URL: "https://ca.example", RootPath: rootPEM, OIDCProviderURL: "https://idp"},
			wantErr: "invalid client name",
		},
		{
			name:    "missing step-ca-url",
			client:  "alice",
			stepCA:  nil,
			wantErr: "step-ca-url",
		},
		{
			name:    "missing oidc-provider",
			client:  "alice",
			stepCA:  &StepCAConfig{URL: "https://ca.example", RootPath: rootPEM},
			wantErr: "oidc-provider",
		},
		{
			name:    "missing step-ca-root",
			client:  "alice",
			stepCA:  &StepCAConfig{URL: "https://ca.example", OIDCProviderURL: "https://idp"},
			wantErr: "step-ca-root",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := IssueClientCertViaOIDC(stateDir, tc.client, tc.stepCA, logger)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestLoadPKIMaterial(t *testing.T) {
	stateDir := filepath.Join(os.TempDir(), "test-state")
	mat := LoadPKIMaterial(stateDir)

	certs := filepath.Join(stateDir, "certs")
	assert.Equal(t, filepath.Join(certs, "ca.crt"), mat.CACertPath)
	assert.Equal(t, filepath.Join(certs, "server.crt"), mat.ServerCertPath)
	assert.Equal(t, filepath.Join(certs, "local-client.crt"), mat.LocalClientCert)
	assert.Equal(t, filepath.Join(certs, "jwt-signing.key"), mat.JWTSigningKey)
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
