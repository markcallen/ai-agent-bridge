package pki

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestNewCertPoolFromPEMAndVerifyOpts(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, err := InitCA("verify-ca", dir)
	if err != nil {
		t.Fatalf("InitCA: %v", err)
	}
	cert, err := LoadCert(certPath)
	if err != nil {
		t.Fatalf("LoadCert: %v", err)
	}
	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	pool := NewCertPoolFromPEM(data)
	if pool == nil {
		t.Fatal("NewCertPoolFromPEM returned nil")
	}

	opts := VerifyOpts(pool)
	if _, err := cert.Verify(opts); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if NewCertPoolFromPEM([]byte("not pem")) != nil {
		t.Fatal("NewCertPoolFromPEM accepted invalid PEM")
	}

	caCert, _, err := LoadCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}
	if _, err := x509.ParseCertificate(caCert.Raw); err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ca.crt")); err != nil {
		t.Fatalf("Stat ca.crt: %v", err)
	}
}
