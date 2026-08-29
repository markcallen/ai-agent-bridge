package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/orchael/bridgectl/internal/localserver"
	"github.com/orchael/bridgectl/internal/pki"
)

// selfSignedCA generates a self-signed CA cert and returns (certPEM, key).
func selfSignedCA(t *testing.T) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key
}

// tlsServerWithCA starts an httptest TLS server signed by the given CA.
func tlsServerWithCA(t *testing.T, caPEM []byte, caKey *ecdsa.PrivateKey) *httptest.Server {
	t.Helper()

	caCert, err := x509.ParseCertificate(mustDecode(t, caPEM))
	if err != nil {
		t.Fatal(err)
	}

	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{
			{
				Certificate: [][]byte{srvDER},
				PrivateKey:  srvKey,
			},
		},
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func mustDecode(t *testing.T, pemData []byte) []byte {
	t.Helper()
	block, _ := pem.Decode(pemData)
	if block == nil {
		t.Fatal("no PEM block")
		return nil
	}
	return block.Bytes
}

func writePEM(t *testing.T, dir string, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifyStepCARoot_valid(t *testing.T) {
	t.Parallel()
	caPEM, caKey := selfSignedCA(t)
	srv := tlsServerWithCA(t, caPEM, caKey)

	dir := t.TempDir()
	rootPath := writePEM(t, dir, "root.crt", caPEM)

	if err := verifyStepCARoot(srv.URL, rootPath); err != nil {
		t.Fatalf("expected valid root to pass: %v", err)
	}
}

func TestVerifyStepCARoot_stale(t *testing.T) {
	t.Parallel()
	// Server is signed by CA-1, but we give the verifier CA-2's root.
	caPEM1, caKey1 := selfSignedCA(t)
	caPEM2, _ := selfSignedCA(t)
	srv := tlsServerWithCA(t, caPEM1, caKey1)

	dir := t.TempDir()
	rootPath := writePEM(t, dir, "root.crt", caPEM2)

	if err := verifyStepCARoot(srv.URL, rootPath); err == nil {
		t.Fatal("expected stale root to fail, got nil")
	}
}

func TestVerifyStepCARoot_missingFile(t *testing.T) {
	t.Parallel()
	if err := verifyStepCARoot("https://localhost:9999", "/nonexistent/root.crt"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestDefaultServerConfigPathUsesStateDirFirst(t *testing.T) {
	stateDir := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	stateConfig := filepath.Join(stateDir, "bridge.yaml")
	writeFile(t, stateConfig)
	xdgConfig := filepath.Join(configHome, "bridgectl", "config.yaml")
	writeFile(t, xdgConfig)

	if got := defaultServerConfigPath(stateDir); got != stateConfig {
		t.Fatalf("defaultServerConfigPath() = %q, want %q", got, stateConfig)
	}
}

func TestDefaultServerConfigPathFallsBackToXDGConfig(t *testing.T) {
	stateDir := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	xdgConfig := filepath.Join(configHome, "bridgectl", "config.yaml")
	writeFile(t, xdgConfig)

	if got := defaultServerConfigPath(stateDir); got != xdgConfig {
		t.Fatalf("defaultServerConfigPath() = %q, want %q", got, xdgConfig)
	}
}

func TestServerRenewCertRejectsPartialExplicitTLSConfig(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("BRIDGECTL_STATE_DIR", stateDir)

	configPath := filepath.Join(t.TempDir(), "bridge.yaml")
	if err := os.WriteFile(configPath, []byte(`
tls:
  cert: /tmp/server.crt
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newServerRenewCertCmd()
	cmd.SetArgs([]string{"--config", configPath})
	cmd.SetOut(nil)
	cmd.SetErr(nil)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want partial TLS config error")
	}
	if !strings.Contains(err.Error(), "must set both tls.cert and tls.key or neither") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestServerRenewCertMergesConfigSANsWithListenAddr(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("BRIDGECTL_STATE_DIR", stateDir)

	// Bootstrap auto-PKI so renew-cert has a CA and server cert to work with.
	logger := slog.Default()
	initialSANs := []string{"server", "127.0.0.1", "localhost"}
	_, err := localserver.EnsurePKI(stateDir, initialSANs, logger, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Write a config that sets both server.listen and server.san.
	configPath := filepath.Join(t.TempDir(), "bridge.yaml")
	if err := os.WriteFile(configPath, []byte(`
server:
  listen: "10.0.0.1:9445"
  san:
    - "vpn.example.com"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newServerRenewCertCmd()
	cmd.SetArgs([]string{"--config", configPath})
	cmd.SetOut(nil)
	cmd.SetErr(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Load the renewed cert and verify SANs include both the listen
	// address IP and the configured SAN, plus defaults.
	cert, err := pki.LoadCert(filepath.Join(stateDir, "certs", "server.crt"))
	if err != nil {
		t.Fatalf("LoadCert: %v", err)
	}

	wantDNS := []string{"vpn.example.com", "server", "localhost"}
	for _, dns := range wantDNS {
		found := false
		for _, san := range cert.DNSNames {
			if san == dns {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("renewed cert missing DNS SAN %q; got %v", dns, cert.DNSNames)
		}
	}

	wantIPs := []string{"10.0.0.1", "127.0.0.1"}
	for _, ipStr := range wantIPs {
		wantIP := net.ParseIP(ipStr)
		found := false
		for _, ip := range cert.IPAddresses {
			if ip.Equal(wantIP) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("renewed cert missing IP SAN %s; got %v", ipStr, cert.IPAddresses)
		}
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
}
