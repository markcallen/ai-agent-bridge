package localserver

import (
	"fmt"
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
	// "server" must be stripped for ACME (public CAs reject bare hostnames).
	mat, err := EnsurePKI(stateDir, []string{"server", "server.example.com"}, logger, stepCfg)
	require.NoError(t, err)

	assert.NotContains(t, *acmeSANs, "server", "ACME must strip 'server' SAN")
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
// provisioner routes to the JWK function variable, and that "server" is
// preserved in the SANs (unlike ACME, JWK / internal Step CA accepts it and
// the client requires it for ServerName verification).
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
			// Pass "server" explicitly — JWK must NOT strip it (clients verify
			// ServerName "server" against the issued cert).
			_, err := EnsurePKI(stateDir, []string{"server", "my-host.example.com"}, logger, stepCfg)
			require.NoError(t, err)
			assert.Contains(t, *jwkSANs, "server", "JWK must preserve 'server' SAN for client TLS verification")
			assert.Contains(t, *jwkSANs, "my-host.example.com")
		})
	}
}

// TestSetCertRenewerFunc verifies that SetCertRenewerFunc correctly overrides
// and restores the renewCertMTLSFn function variable.
func TestSetCertRenewerFunc(t *testing.T) {
	original := renewCertMTLSFn

	called := false
	restore := SetCertRenewerFunc(func(_ *StepCAConfig, _, _ string, _ *slog.Logger) error {
		called = true
		return nil
	})

	// Call through the function variable — it should call our override.
	err := renewCertMTLSFn(nil, "", "", nil)
	require.NoError(t, err)
	assert.True(t, called, "override should have been called")

	// Restore and verify the original is back.
	restore()
	// We can't call the original (it needs a real Step CA), but we can verify
	// the function pointer was restored.
	assert.Equal(t,
		fmt.Sprintf("%p", original),
		fmt.Sprintf("%p", renewCertMTLSFn),
		"restore should put back the original function",
	)
}

// TestSetCertRenewerFunc_NilNoOp verifies that passing nil to
// SetCertRenewerFunc does not change the function variable.
func TestSetCertRenewerFunc_NilNoOp(t *testing.T) {
	before := fmt.Sprintf("%p", renewCertMTLSFn)
	restore := SetCertRenewerFunc(nil)
	after := fmt.Sprintf("%p", renewCertMTLSFn)
	assert.Equal(t, before, after, "nil should not change the function variable")
	restore()
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
