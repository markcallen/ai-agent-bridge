package pki

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestInitCA(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, err := InitCA("test-ca", dir)
	if err != nil {
		t.Fatalf("InitCA: %v", err)
	}

	cert, key, err := LoadCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}

	if !cert.IsCA {
		t.Error("cert is not CA")
	}
	if cert.Subject.CommonName != "test-ca" {
		t.Errorf("CN = %q, want %q", cert.Subject.CommonName, "test-ca")
	}
	if key == nil {
		t.Error("key is nil")
	}
}

func TestIssueCert(t *testing.T) {
	dir := t.TempDir()
	_, _, err := InitCA("test-ca", dir)
	if err != nil {
		t.Fatalf("InitCA: %v", err)
	}

	caCert, caKey, err := LoadCA(filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key"))
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}

	certPath, _, err := IssueCert(caCert, caKey, CertTypeServer, "bridge.local", []string{"bridge.local", "127.0.0.1"}, dir)
	if err != nil {
		t.Fatalf("IssueCert: %v", err)
	}

	cert, err := LoadCert(certPath)
	if err != nil {
		t.Fatalf("LoadCert: %v", err)
	}

	if cert.Subject.CommonName != "bridge.local" {
		t.Errorf("CN = %q, want %q", cert.Subject.CommonName, "bridge.local")
	}
	if len(cert.DNSNames) == 0 || cert.DNSNames[0] != "bridge.local" {
		t.Errorf("DNSNames = %v, want [bridge.local]", cert.DNSNames)
	}
	if len(cert.IPAddresses) == 0 || cert.IPAddresses[0].String() != "127.0.0.1" {
		t.Errorf("IPAddresses = %v, want [127.0.0.1]", cert.IPAddresses)
	}

	// Verify cert is signed by CA
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Errorf("cert verification failed: %v", err)
	}
}

func TestCrossSign(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	InitCA("ca-a", dirA)
	InitCA("ca-b", dirB)

	caCertA, caKeyA, _ := LoadCA(filepath.Join(dirA, "ca.crt"), filepath.Join(dirA, "ca.key"))
	caCertB, _, _ := LoadCA(filepath.Join(dirB, "ca.crt"), filepath.Join(dirB, "ca.key"))

	outPath := filepath.Join(dirA, "ca-b-cross-signed.crt")
	if err := CrossSign(caCertA, caKeyA, caCertB, outPath); err != nil {
		t.Fatalf("CrossSign: %v", err)
	}

	crossSigned, err := LoadCert(outPath)
	if err != nil {
		t.Fatalf("LoadCert: %v", err)
	}

	if crossSigned.Subject.CommonName != "ca-b" {
		t.Errorf("CN = %q, want %q", crossSigned.Subject.CommonName, "ca-b")
	}
	if !crossSigned.IsCA {
		t.Error("cross-signed cert should be CA")
	}
}

func TestBuildBundle(t *testing.T) {
	dir := t.TempDir()
	InitCA("ca-1", dir)

	dir2 := t.TempDir()
	InitCA("ca-2", dir2)

	bundlePath := filepath.Join(dir, "bundle.crt")
	err := BuildBundle(bundlePath, filepath.Join(dir, "ca.crt"), filepath.Join(dir2, "ca.crt"))
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}

	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}

	// Should contain two certificates
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		t.Error("failed to parse bundle")
	}
}

func TestJWTKeypair(t *testing.T) {
	dir := t.TempDir()
	pubPath, privPath, err := GenerateJWTKeypair(dir, "jwt-test")
	if err != nil {
		t.Fatalf("GenerateJWTKeypair: %v", err)
	}

	priv, err := LoadEd25519PrivateKey(privPath)
	if err != nil {
		t.Fatalf("LoadEd25519PrivateKey: %v", err)
	}
	if priv == nil {
		t.Error("private key is nil")
	}

	pub, err := LoadEd25519PublicKey(pubPath)
	if err != nil {
		t.Fatalf("LoadEd25519PublicKey: %v", err)
	}
	if pub == nil {
		t.Error("public key is nil")
	}
}
