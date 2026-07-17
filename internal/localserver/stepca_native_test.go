package localserver

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCertRequester returns a function that writes placeholder cert and key
// files and records which SANs were requested.
func fakeCertRequester(t *testing.T) (func(*StepCAConfig, []string, string, string, *slog.Logger) error, *[]string) {
	t.Helper()
	var capturedSANs []string
	fn := func(_ *StepCAConfig, sans []string, certPath, keyPath string, _ *slog.Logger) error {
		capturedSANs = append(capturedSANs, sans...)
		require.NoError(t, os.WriteFile(certPath, []byte("FAKE-CERT"), 0o644))
		require.NoError(t, os.WriteFile(keyPath, []byte("FAKE-KEY"), 0o600))
		return nil
	}
	return fn, &capturedSANs
}

// TestProvisionerRouting_ACME verifies that provisioner "acme" routes to the
// ACME function variable.
func TestProvisionerRouting_ACME(t *testing.T) {
	stateDir := t.TempDir()
	certsDir := filepath.Join(stateDir, "certs")
	require.NoError(t, os.MkdirAll(certsDir, 0o700))

	// Write a fake root cert for the CA bundle copy.
	rootPath := filepath.Join(t.TempDir(), "root.crt")
	require.NoError(t, os.WriteFile(rootPath, []byte("FAKE-ROOT"), 0o644))

	// Override ACME function variable.
	acmeFn, acmeSANs := fakeCertRequester(t)
	oldACME := requestCertACMEFn
	requestCertACMEFn = acmeFn
	t.Cleanup(func() { requestCertACMEFn = oldACME })

	// Override JWK function variable — should NOT be called.
	oldJWK := requestCertJWKFn
	requestCertJWKFn = func(_ *StepCAConfig, _ []string, _, _ string, _ *slog.Logger) error {
		t.Fatal("JWK function should not be called for ACME provisioner")
		return nil
	}
	t.Cleanup(func() { requestCertJWKFn = oldJWK })

	stepCfg := &StepCAConfig{
		URL:         "https://ca.example.com",
		RootPath:    rootPath,
		Provisioner: "acme",
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	mat, err := EnsurePKI(stateDir, []string{"server.example.com"}, logger, stepCfg)
	require.NoError(t, err)

	assert.Contains(t, *acmeSANs, "server.example.com")
	assert.FileExists(t, mat.ServerCertPath)
	assert.FileExists(t, mat.ServerKeyPath)
}

// TestProvisionerRouting_ACME_CaseInsensitive verifies case-insensitive routing.
func TestProvisionerRouting_ACME_CaseInsensitive(t *testing.T) {
	stateDir := t.TempDir()
	certsDir := filepath.Join(stateDir, "certs")
	require.NoError(t, os.MkdirAll(certsDir, 0o700))

	rootPath := filepath.Join(t.TempDir(), "root.crt")
	require.NoError(t, os.WriteFile(rootPath, []byte("FAKE-ROOT"), 0o644))

	acmeCalled := false
	oldACME := requestCertACMEFn
	requestCertACMEFn = func(_ *StepCAConfig, _ []string, certPath, keyPath string, _ *slog.Logger) error {
		acmeCalled = true
		_ = os.WriteFile(certPath, []byte("FAKE-CERT"), 0o644)
		_ = os.WriteFile(keyPath, []byte("FAKE-KEY"), 0o600)
		return nil
	}
	t.Cleanup(func() { requestCertACMEFn = oldACME })

	oldJWK := requestCertJWKFn
	requestCertJWKFn = func(_ *StepCAConfig, _ []string, _, _ string, _ *slog.Logger) error {
		t.Fatal("JWK should not be called for ACME provisioner")
		return nil
	}
	t.Cleanup(func() { requestCertJWKFn = oldJWK })

	stepCfg := &StepCAConfig{
		URL:         "https://ca.example.com",
		RootPath:    rootPath,
		Provisioner: "ACME",
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	_, err := EnsurePKI(stateDir, []string{"server"}, logger, stepCfg)
	require.NoError(t, err)
	assert.True(t, acmeCalled, "ACME function should have been called")
}

// TestProvisionerRouting_JWK verifies that an empty provisioner or a named JWK
// provisioner routes to the JWK function variable.
func TestProvisionerRouting_JWK(t *testing.T) {
	for _, provName := range []string{"", "bridge-jwk", "my-provisioner"} {
		t.Run("provisioner="+provName, func(t *testing.T) {
			stateDir := t.TempDir()
			certsDir := filepath.Join(stateDir, "certs")
			require.NoError(t, os.MkdirAll(certsDir, 0o700))

			rootPath := filepath.Join(t.TempDir(), "root.crt")
			require.NoError(t, os.WriteFile(rootPath, []byte("FAKE-ROOT"), 0o644))

			jwkFn, jwkSANs := fakeCertRequester(t)
			oldJWK := requestCertJWKFn
			requestCertJWKFn = jwkFn
			t.Cleanup(func() { requestCertJWKFn = oldJWK })

			oldACME := requestCertACMEFn
			requestCertACMEFn = func(_ *StepCAConfig, _ []string, _, _ string, _ *slog.Logger) error {
				t.Fatal("ACME should not be called for JWK provisioner")
				return nil
			}
			t.Cleanup(func() { requestCertACMEFn = oldACME })

			stepCfg := &StepCAConfig{
				URL:         "https://ca.example.com",
				RootPath:    rootPath,
				Provisioner: provName,
			}

			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
			_, err := EnsurePKI(stateDir, []string{"server"}, logger, stepCfg)
			require.NoError(t, err)
			assert.Contains(t, *jwkSANs, "server")
		})
	}
}

// TestReadProvisionerPassword_File verifies reading password from a file.
func TestReadProvisionerPassword_File(t *testing.T) {
	pwFile := filepath.Join(t.TempDir(), "pw.txt")
	require.NoError(t, os.WriteFile(pwFile, []byte("my-secret\n"), 0o600))

	pw, err := readProvisionerPassword(pwFile)
	require.NoError(t, err)
	assert.Equal(t, "my-secret", string(pw))
}

// TestReadProvisionerPassword_MissingFile verifies error on missing file.
func TestReadProvisionerPassword_MissingFile(t *testing.T) {
	_, err := readProvisionerPassword("/nonexistent/path/pw.txt")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read password file")
}
