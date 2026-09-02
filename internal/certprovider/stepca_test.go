package certprovider

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/orchael/bridgectl/internal/pki"
)

// setupStepCATestPKI creates a CA + server cert that the mock functions
// can "return" by copying into the target paths.
func setupStepCATestPKI(t *testing.T) (caCertPath string, srcCertPath string, srcKeyPath string) {
	t.Helper()
	dir := t.TempDir()
	caCertPath, caKeyPath, err := pki.InitCA("test-stepca", dir)
	if err != nil {
		t.Fatalf("InitCA: %v", err)
	}
	caCert, caKey, err := pki.LoadCA(caCertPath, caKeyPath)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}
	serverDir := filepath.Join(dir, "server")
	srcCertPath, srcKeyPath, err = pki.IssueCert(caCert, caKey, pki.CertTypeServer, "bridge.local", []string{"bridge.local"}, serverDir, 0)
	if err != nil {
		t.Fatalf("IssueCert: %v", err)
	}
	return caCertPath, srcCertPath, srcKeyPath
}

// copyFile is a test helper that copies src to dst.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(dst), err)
	}
	perm := os.FileMode(0o644)
	if filepath.Ext(dst) == ".key" {
		perm = 0o600
	}
	if err := os.WriteFile(dst, data, perm); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

func TestStepCAProvider_NewRequiresURL(t *testing.T) {
	_, err := NewStepCAProvider(StepCAConfig{RootPath: "/ca.crt"}, nil)
	if err == nil {
		t.Error("expected error for missing URL")
	}
}

func TestStepCAProvider_NewRequiresRoot(t *testing.T) {
	_, err := NewStepCAProvider(StepCAConfig{URL: "https://ca.local"}, nil)
	if err == nil {
		t.Error("expected error for missing root path")
	}
}

func TestStepCAProvider_EnrollJWK(t *testing.T) {
	caCertPath, srcCertPath, srcKeyPath := setupStepCATestPKI(t)
	outDir := t.TempDir()

	p, err := NewStepCAProvider(StepCAConfig{
		URL:      "https://ca.local",
		RootPath: caCertPath,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewStepCAProvider: %v", err)
	}

	// Mock the JWK request function to copy pre-generated certs.
	p.RequestJWK = func(cfg *StepCAConfig, sans []string, certPath, keyPath string, logger *slog.Logger) error {
		copyFile(t, srcCertPath, certPath)
		copyFile(t, srcKeyPath, keyPath)
		return nil
	}

	id, err := p.Enroll(context.Background(), EnrollmentRequest{
		CommonName: "bridge.local",
		SANs:       []string{"bridge.local"},
		Role:       RoleServer,
		OutDir:     outDir,
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if id.Provider != "stepca" {
		t.Errorf("Provider = %q, want %q", id.Provider, "stepca")
	}
	if id.CommonName != "bridge.local" {
		t.Errorf("CommonName = %q, want %q", id.CommonName, "bridge.local")
	}
	if !id.Renewable {
		t.Error("stepca identity should be renewable")
	}
}

func TestStepCAProvider_EnrollACME(t *testing.T) {
	caCertPath, srcCertPath, srcKeyPath := setupStepCATestPKI(t)
	outDir := t.TempDir()

	p, err := NewStepCAProvider(StepCAConfig{
		URL:         "https://ca.local",
		RootPath:    caCertPath,
		Provisioner: "acme",
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewStepCAProvider: %v", err)
	}

	p.RequestACME = func(cfg *StepCAConfig, sans []string, certPath, keyPath string, logger *slog.Logger) error {
		copyFile(t, srcCertPath, certPath)
		copyFile(t, srcKeyPath, keyPath)
		return nil
	}

	id, err := p.Enroll(context.Background(), EnrollmentRequest{
		CommonName: "bridge.local",
		SANs:       []string{"bridge.local"},
		Role:       RoleServer,
		OutDir:     outDir,
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if id.Provider != "stepca" {
		t.Errorf("Provider = %q, want %q", id.Provider, "stepca")
	}
}

func TestStepCAProvider_EnrollFailure(t *testing.T) {
	caCertPath, _, _ := setupStepCATestPKI(t)

	p, err := NewStepCAProvider(StepCAConfig{
		URL:      "https://ca.local",
		RootPath: caCertPath,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewStepCAProvider: %v", err)
	}

	p.RequestJWK = func(cfg *StepCAConfig, sans []string, certPath, keyPath string, logger *slog.Logger) error {
		return errors.New("provisioner auth failed")
	}

	_, err = p.Enroll(context.Background(), EnrollmentRequest{
		CommonName: "bridge.local",
		SANs:       []string{"bridge.local"},
		OutDir:     t.TempDir(),
	})
	if err == nil {
		t.Error("expected enrollment failure")
	}
}

func TestStepCAProvider_EnrollRequiresOutDir(t *testing.T) {
	caCertPath, _, _ := setupStepCATestPKI(t)

	p, err := NewStepCAProvider(StepCAConfig{
		URL:      "https://ca.local",
		RootPath: caCertPath,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewStepCAProvider: %v", err)
	}
	p.RequestJWK = func(cfg *StepCAConfig, sans []string, certPath, keyPath string, logger *slog.Logger) error {
		return nil
	}

	_, err = p.Enroll(context.Background(), EnrollmentRequest{
		CommonName: "bridge.local",
	})
	if err == nil {
		t.Error("expected error for missing OutDir")
	}
}

func TestStepCAProvider_RenewMTLS(t *testing.T) {
	caCertPath, srcCertPath, srcKeyPath := setupStepCATestPKI(t)
	outDir := t.TempDir()

	// Set up identity files in outDir.
	certPath := filepath.Join(outDir, "bridge.local.crt")
	keyPath := filepath.Join(outDir, "bridge.local.key")
	copyFile(t, srcCertPath, certPath)
	copyFile(t, srcKeyPath, keyPath)

	p, err := NewStepCAProvider(StepCAConfig{
		URL:      "https://ca.local",
		RootPath: caCertPath,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewStepCAProvider: %v", err)
	}

	renewCalled := false
	p.RenewMTLS = func(cfg *StepCAConfig, cp, kp string, logger *slog.Logger) error {
		renewCalled = true
		// Simulate renewal by writing the same cert (in reality it would be a new one).
		copyFile(t, srcCertPath, cp)
		return nil
	}

	id := &Identity{
		CertPath:   certPath,
		KeyPath:    keyPath,
		CACertPath: caCertPath,
		Provider:   "stepca",
	}

	err = p.Renew(context.Background(), id)
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if !renewCalled {
		t.Error("expected mTLS renewal to be called")
	}
}

func TestStepCAProvider_RenewFallsBackToJWK(t *testing.T) {
	caCertPath, srcCertPath, srcKeyPath := setupStepCATestPKI(t)
	outDir := t.TempDir()

	certPath := filepath.Join(outDir, "bridge.local.crt")
	keyPath := filepath.Join(outDir, "bridge.local.key")
	copyFile(t, srcCertPath, certPath)
	copyFile(t, srcKeyPath, keyPath)

	p, err := NewStepCAProvider(StepCAConfig{
		URL:                     "https://ca.local",
		RootPath:                caCertPath,
		ProvisionerPasswordFile: "/tmp/pw", // required for JWK fallback
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewStepCAProvider: %v", err)
	}

	// mTLS renewal fails → should fall back to JWK.
	p.RenewMTLS = func(cfg *StepCAConfig, cp, kp string, logger *slog.Logger) error {
		return errors.New("mTLS renewal: cert expired")
	}

	jwkCalled := false
	p.RequestJWK = func(cfg *StepCAConfig, sans []string, cp, kp string, logger *slog.Logger) error {
		jwkCalled = true
		copyFile(t, srcCertPath, cp)
		copyFile(t, srcKeyPath, kp)
		return nil
	}

	id := &Identity{
		CertPath:   certPath,
		KeyPath:    keyPath,
		CACertPath: caCertPath,
		Provider:   "stepca",
	}

	err = p.Renew(context.Background(), id)
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if !jwkCalled {
		t.Error("expected JWK fallback to be called after mTLS failure")
	}
}

func TestStepCAProvider_RenewNeverInsecureFallback(t *testing.T) {
	caCertPath, srcCertPath, srcKeyPath := setupStepCATestPKI(t)
	outDir := t.TempDir()

	certPath := filepath.Join(outDir, "bridge.local.crt")
	keyPath := filepath.Join(outDir, "bridge.local.key")
	copyFile(t, srcCertPath, certPath)
	copyFile(t, srcKeyPath, keyPath)

	p, err := NewStepCAProvider(StepCAConfig{
		URL:      "https://ca.local",
		RootPath: caCertPath,
		// No password file — JWK fallback should fail
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewStepCAProvider: %v", err)
	}

	p.RenewMTLS = func(cfg *StepCAConfig, cp, kp string, logger *slog.Logger) error {
		return errors.New("mTLS renewal failed")
	}
	p.RequestJWK = func(cfg *StepCAConfig, sans []string, cp, kp string, logger *slog.Logger) error {
		return errors.New("should not reach JWK without password file")
	}

	id := &Identity{
		CertPath:   certPath,
		KeyPath:    keyPath,
		CACertPath: caCertPath,
		Provider:   "stepca",
	}

	err = p.Renew(context.Background(), id)
	if err == nil {
		t.Error("expected renewal error when both paths fail — should never fall back to insecure")
	}
}

func TestStepCAProvider_Roots(t *testing.T) {
	caCertPath, _, _ := setupStepCATestPKI(t)

	p, err := NewStepCAProvider(StepCAConfig{
		URL:      "https://ca.local",
		RootPath: caCertPath,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewStepCAProvider: %v", err)
	}

	roots, err := p.Roots(context.Background())
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("Roots returned %d certs, want 1", len(roots))
	}
	if roots[0].Subject.CommonName != "test-stepca" {
		t.Errorf("Root CN = %q, want %q", roots[0].Subject.CommonName, "test-stepca")
	}
}
