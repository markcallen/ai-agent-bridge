package certprovider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/orchael/bridgectl/internal/pki"
)

// setupTestPKI creates a CA and issues a client certificate in a temp dir.
func setupTestPKI(t *testing.T) (caCertPath, certPath, keyPath string) {
	t.Helper()
	dir := t.TempDir()

	caCertPath, caKeyPath, err := pki.InitCA("test-ca", dir)
	if err != nil {
		t.Fatalf("InitCA: %v", err)
	}

	caCert, caKey, err := pki.LoadCA(caCertPath, caKeyPath)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}

	clientDir := filepath.Join(dir, "client")
	certPath, keyPath, err = pki.IssueCert(caCert, caKey, pki.CertTypeClient, "test-client", nil, clientDir, 0)
	if err != nil {
		t.Fatalf("IssueCert: %v", err)
	}

	return caCertPath, certPath, keyPath
}

func TestFilesystemProvider_ValidMaterial(t *testing.T) {
	caCert, cert, key := setupTestPKI(t)

	p, err := NewFilesystemProvider(FilesystemConfig{
		CACertPath: caCert,
		CertPath:   cert,
		KeyPath:    key,
	})
	if err != nil {
		t.Fatalf("NewFilesystemProvider: %v", err)
	}
	if p == nil {
		t.Fatal("provider is nil")
	}
}

func TestFilesystemProvider_Enroll(t *testing.T) {
	caCert, cert, key := setupTestPKI(t)

	p, err := NewFilesystemProvider(FilesystemConfig{
		CACertPath: caCert,
		CertPath:   cert,
		KeyPath:    key,
	})
	if err != nil {
		t.Fatalf("NewFilesystemProvider: %v", err)
	}

	id, err := p.Enroll(context.Background(), EnrollmentRequest{})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if id.Provider != "filesystem" {
		t.Errorf("Provider = %q, want %q", id.Provider, "filesystem")
	}
	if id.CommonName != "test-client" {
		t.Errorf("CommonName = %q, want %q", id.CommonName, "test-client")
	}
	if id.Renewable {
		t.Error("filesystem identity should not be renewable")
	}
	if id.CertPath != cert {
		t.Errorf("CertPath = %q, want %q", id.CertPath, cert)
	}
	if id.KeyPath != key {
		t.Errorf("KeyPath = %q, want %q", id.KeyPath, key)
	}
	if id.CACertPath != caCert {
		t.Errorf("CACertPath = %q, want %q", id.CACertPath, caCert)
	}
	if id.NotAfter.IsZero() {
		t.Error("NotAfter should not be zero")
	}
}

func TestFilesystemProvider_Renew_NotSupported(t *testing.T) {
	caCert, cert, key := setupTestPKI(t)

	p, err := NewFilesystemProvider(FilesystemConfig{
		CACertPath: caCert,
		CertPath:   cert,
		KeyPath:    key,
	})
	if err != nil {
		t.Fatalf("NewFilesystemProvider: %v", err)
	}

	err = p.Renew(context.Background(), &Identity{})
	if !errors.Is(err, ErrRenewalNotSupported) {
		t.Errorf("Renew err = %v, want ErrRenewalNotSupported", err)
	}
}

func TestFilesystemProvider_Roots(t *testing.T) {
	caCert, cert, key := setupTestPKI(t)

	p, err := NewFilesystemProvider(FilesystemConfig{
		CACertPath: caCert,
		CertPath:   cert,
		KeyPath:    key,
	})
	if err != nil {
		t.Fatalf("NewFilesystemProvider: %v", err)
	}

	roots, err := p.Roots(context.Background())
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("Roots returned %d certs, want 1", len(roots))
	}
	if roots[0].Subject.CommonName != "test-ca" {
		t.Errorf("Root CN = %q, want %q", roots[0].Subject.CommonName, "test-ca")
	}
}

func TestFilesystemProvider_MissingFiles(t *testing.T) {
	caCert, cert, key := setupTestPKI(t)

	tests := []struct {
		name string
		cfg  FilesystemConfig
	}{
		{"missing CA", FilesystemConfig{CACertPath: "/nonexistent/ca.pem", CertPath: cert, KeyPath: key}},
		{"missing cert", FilesystemConfig{CACertPath: caCert, CertPath: "/nonexistent/cert.pem", KeyPath: key}},
		{"missing key", FilesystemConfig{CACertPath: caCert, CertPath: cert, KeyPath: "/nonexistent/key.pem"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewFilesystemProvider(tt.cfg)
			if err == nil {
				t.Error("expected error for missing file")
			}
		})
	}
}

func TestFilesystemProvider_RequiredPaths(t *testing.T) {
	tests := []struct {
		name string
		cfg  FilesystemConfig
	}{
		{"empty CA", FilesystemConfig{CACertPath: "", CertPath: "c", KeyPath: "k"}},
		{"empty cert", FilesystemConfig{CACertPath: "ca", CertPath: "", KeyPath: "k"}},
		{"empty key", FilesystemConfig{CACertPath: "ca", CertPath: "c", KeyPath: ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewFilesystemProvider(tt.cfg)
			if err == nil {
				t.Error("expected error for missing required path")
			}
		})
	}
}

func TestFilesystemProvider_MalformedPEM(t *testing.T) {
	dir := t.TempDir()

	garbage := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(garbage, []byte("not a pem file"), 0o644); err != nil {
		t.Fatal(err)
	}

	caCert, cert, key := setupTestPKI(t)

	tests := []struct {
		name string
		cfg  FilesystemConfig
	}{
		{"malformed CA", FilesystemConfig{CACertPath: garbage, CertPath: cert, KeyPath: key}},
		{"malformed cert", FilesystemConfig{CACertPath: caCert, CertPath: garbage, KeyPath: key}},
		{"malformed key", FilesystemConfig{CACertPath: caCert, CertPath: cert, KeyPath: garbage}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewFilesystemProvider(tt.cfg)
			if err == nil {
				t.Error("expected error for malformed PEM")
			}
		})
	}
}

func TestFilesystemProvider_MismatchedKeyAndCert(t *testing.T) {
	dir := t.TempDir()

	ca1Dir := filepath.Join(dir, "ca1")
	ca1CertPath, ca1KeyPath, err := pki.InitCA("ca1", ca1Dir)
	if err != nil {
		t.Fatalf("InitCA 1: %v", err)
	}
	ca1Cert, ca1Key, err := pki.LoadCA(ca1CertPath, ca1KeyPath)
	if err != nil {
		t.Fatalf("LoadCA 1: %v", err)
	}

	ca2Dir := filepath.Join(dir, "ca2")
	ca2CertPath, ca2KeyPath, err := pki.InitCA("ca2", ca2Dir)
	if err != nil {
		t.Fatalf("InitCA 2: %v", err)
	}
	ca2Cert, ca2Key, err := pki.LoadCA(ca2CertPath, ca2KeyPath)
	if err != nil {
		t.Fatalf("LoadCA 2: %v", err)
	}

	cert1Dir := filepath.Join(dir, "cert1")
	cert1Path, _, err := pki.IssueCert(ca1Cert, ca1Key, pki.CertTypeClient, "client1", nil, cert1Dir, 0)
	if err != nil {
		t.Fatalf("IssueCert 1: %v", err)
	}

	cert2Dir := filepath.Join(dir, "cert2")
	_, key2Path, err := pki.IssueCert(ca2Cert, ca2Key, pki.CertTypeClient, "client2", nil, cert2Dir, 0)
	if err != nil {
		t.Fatalf("IssueCert 2: %v", err)
	}

	_, err = NewFilesystemProvider(FilesystemConfig{
		CACertPath: ca1CertPath,
		CertPath:   cert1Path,
		KeyPath:    key2Path,
	})
	if err == nil {
		t.Error("expected error for mismatched cert/key")
	}
}

func TestFilesystemProvider_CertNotChainedToCA(t *testing.T) {
	dir := t.TempDir()

	ca1Dir := filepath.Join(dir, "ca1")
	ca1CertPath, ca1KeyPath, err := pki.InitCA("ca1", ca1Dir)
	if err != nil {
		t.Fatalf("InitCA 1: %v", err)
	}
	ca1Cert, ca1Key, err := pki.LoadCA(ca1CertPath, ca1KeyPath)
	if err != nil {
		t.Fatalf("LoadCA 1: %v", err)
	}

	ca2Dir := filepath.Join(dir, "ca2")
	ca2CertPath, _, err := pki.InitCA("ca2", ca2Dir)
	if err != nil {
		t.Fatalf("InitCA 2: %v", err)
	}

	clientDir := filepath.Join(dir, "client")
	certPath, keyPath, err := pki.IssueCert(ca1Cert, ca1Key, pki.CertTypeClient, "client", nil, clientDir, 0)
	if err != nil {
		t.Fatalf("IssueCert: %v", err)
	}

	// Cert from CA1, but trust root is CA2 — should fail chain verification.
	_, err = NewFilesystemProvider(FilesystemConfig{
		CACertPath: ca2CertPath,
		CertPath:   certPath,
		KeyPath:    keyPath,
	})
	if err == nil {
		t.Error("expected error: cert does not chain to configured CA")
	}
}

func TestFilesystemProvider_CABundleMultipleCerts(t *testing.T) {
	dir := t.TempDir()

	ca1Dir := filepath.Join(dir, "ca1")
	ca1CertPath, ca1KeyPath, err := pki.InitCA("ca1", ca1Dir)
	if err != nil {
		t.Fatalf("InitCA 1: %v", err)
	}
	ca1Cert, ca1Key, err := pki.LoadCA(ca1CertPath, ca1KeyPath)
	if err != nil {
		t.Fatalf("LoadCA 1: %v", err)
	}

	ca2Dir := filepath.Join(dir, "ca2")
	ca2CertPath, _, err := pki.InitCA("ca2", ca2Dir)
	if err != nil {
		t.Fatalf("InitCA 2: %v", err)
	}

	// Build a combined bundle with both CAs.
	bundlePath := filepath.Join(dir, "ca-bundle.crt")
	ca1PEM, _ := os.ReadFile(ca1CertPath)
	ca2PEM, _ := os.ReadFile(ca2CertPath)
	if err := os.WriteFile(bundlePath, append(ca1PEM, ca2PEM...), 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	clientDir := filepath.Join(dir, "client")
	certPath, keyPath, err := pki.IssueCert(ca1Cert, ca1Key, pki.CertTypeClient, "client", nil, clientDir, 0)
	if err != nil {
		t.Fatalf("IssueCert: %v", err)
	}

	p, err := NewFilesystemProvider(FilesystemConfig{
		CACertPath: bundlePath,
		CertPath:   certPath,
		KeyPath:    keyPath,
	})
	if err != nil {
		t.Fatalf("NewFilesystemProvider: %v", err)
	}

	roots, err := p.Roots(context.Background())
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	if len(roots) != 2 {
		t.Fatalf("Roots returned %d certs, want 2", len(roots))
	}

	names := map[string]bool{}
	for _, r := range roots {
		names[r.Subject.CommonName] = true
	}
	if !names["ca1"] || !names["ca2"] {
		t.Errorf("expected ca1 and ca2 in roots, got %v", names)
	}
}

func TestFilesystemProvider_KeyPermissions(t *testing.T) {
	caCert, cert, key := setupTestPKI(t)

	// Make the key world-readable.
	if err := os.Chmod(key, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	_, err := NewFilesystemProvider(FilesystemConfig{
		CACertPath: caCert,
		CertPath:   cert,
		KeyPath:    key,
	})
	if err == nil {
		t.Error("expected error for overly permissive key file")
	}
}

func TestLoadCertBundle_NoCerts(t *testing.T) {
	dir := t.TempDir()
	// PEM file with a non-certificate block — should return error.
	noCtPEM := filepath.Join(dir, "noct.pem")
	if err := os.WriteFile(noCtPEM, []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIBogIBAAJBAL==\n-----END RSA PRIVATE KEY-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := loadCertBundle(noCtPEM)
	if err == nil {
		t.Error("expected error for PEM with no CERTIFICATE blocks")
	}
}

func TestIdentity_NeedsRenewal(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name   string
		before time.Time
		after  time.Time
		want   bool
	}{
		{
			name:   "fresh cert (80 of 90 days remaining)",
			before: now.AddDate(0, 0, -10),
			after:  now.AddDate(0, 0, 80),
			want:   false,
		},
		{
			name:   "near expiry (10 of 90 days remaining)",
			before: now.AddDate(0, 0, -80),
			after:  now.AddDate(0, 0, 10),
			want:   true,
		},
		{
			name:   "just above threshold (slightly more than 1/3 remaining)",
			before: now.AddDate(0, 0, -59),
			after:  now.AddDate(0, 0, 31),
			want:   false,
		},
		{
			name:   "expired",
			before: now.AddDate(0, 0, -100),
			after:  now.AddDate(0, 0, -10),
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := &Identity{
				NotBefore: tt.before,
				NotAfter:  tt.after,
			}
			if got := id.NeedsRenewal(); got != tt.want {
				t.Errorf("NeedsRenewal() = %v, want %v", got, tt.want)
			}
		})
	}
}
