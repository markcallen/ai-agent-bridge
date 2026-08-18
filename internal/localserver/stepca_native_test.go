package localserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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
	// "server" must be kept because clients verify TLS with ServerName "server".
	// Loopback entries ("localhost", "127.0.0.1") are still stripped.
	mat, err := EnsurePKI(stateDir, []string{"server", "server.example.com"}, logger, stepCfg, 0)
	require.NoError(t, err)

	assert.Contains(t, *acmeSANs, "server", "ACME must keep 'server' SAN for client TLS verification")
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
	_, err := EnsurePKI(stateDir, []string{"server"}, logger, stepCfg, 0)
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
			_, err := EnsurePKI(stateDir, []string{"server", "my-host.example.com"}, logger, stepCfg, 0)
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

// TestGetClientCertificate_LeafOnlyPEM is a regression test for the "missing
// client certificate" bug. When the cert PEM contains only the leaf (no
// intermediate chain), tls.Config.Certificates fails to match the cert
// against the server's acceptable-CA list (which contains the root, not the
// intermediate). GetClientCertificate bypasses that matching and always
// presents the cert.
func TestGetClientCertificate_LeafOnlyPEM(t *testing.T) {
	// Create a root CA.
	rootKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)
	rootTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Root CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	require.NoError(t, err)
	rootCert, err := x509.ParseCertificate(rootDER)
	require.NoError(t, err)

	// Create an intermediate CA signed by root.
	interKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)
	interTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "Test Intermediate CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	interDER, err := x509.CreateCertificate(rand.Reader, interTmpl, rootCert, &interKey.PublicKey, rootKey)
	require.NoError(t, err)
	interCert, err := x509.ParseCertificate(interDER)
	require.NoError(t, err)

	// Issue a leaf cert signed by the intermediate.
	leafKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "leaf"},
		DNSNames:     []string{"leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, interCert, &leafKey.PublicKey, interKey)
	require.NoError(t, err)

	// Write leaf-only PEM (no intermediate chain) and key to temp files.
	dir := t.TempDir()
	certPath := filepath.Join(dir, "leaf.crt")
	keyPath := filepath.Join(dir, "leaf.key")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	require.NoError(t, os.WriteFile(certPath, certPEM, 0o644))

	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	// Server CA pool contains only the root — this is what Step CA does.
	// The intermediate's subject != root's subject, so a leaf-only chain
	// has no cert whose issuer matches the root's subject.
	clientCAPool := x509.NewCertPool()
	clientCAPool.AddCert(rootCert)

	// Issue a server cert for the test TLS server.
	serverKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)
	serverTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(4),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTmpl, rootCert, &serverKey.PublicKey, rootKey)
	require.NoError(t, err)

	serverTLSCert := tls.Certificate{
		Certificate: [][]byte{serverDER},
		PrivateKey:  serverKey,
	}

	// Helper: start a TLS server that records whether a client cert was received.
	// Uses RequestClientCert so the server sends a CertificateRequest with the
	// root-only ClientCAs (triggering Go's acceptable-CA matching on the client)
	// but does not reject the connection when the cert is missing or unverifiable.
	startServer := func(t *testing.T) (*httptest.Server, *atomic.Bool) {
		t.Helper()
		sawClientCert := &atomic.Bool{}
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
				sawClientCert.Store(true)
			}
			w.WriteHeader(http.StatusOK)
		})
		srv := httptest.NewUnstartedServer(handler)
		srv.TLS = &tls.Config{
			Certificates: []tls.Certificate{serverTLSCert},
			ClientAuth:   tls.RequestClientCert,
			ClientCAs:    clientCAPool,
			MinVersion:   tls.VersionTLS12,
		}
		srv.StartTLS()
		t.Cleanup(srv.Close)
		return srv, sawClientCert
	}

	// Load the leaf-only cert+key.
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	require.NoError(t, err)

	serverPool := x509.NewCertPool()
	serverPool.AddCert(rootCert)

	t.Run("Certificates_does_not_send_leaf_only_cert", func(t *testing.T) {
		srv, sawCert := startServer(t)
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      serverPool,
				MinVersion:   tls.VersionTLS12,
			},
		}
		client := &http.Client{Transport: tr}
		resp, err := client.Get(srv.URL)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.False(t, sawCert.Load(),
			"Certificates should NOT send a leaf-only cert when the server's "+
				"acceptable-CA list contains only the root")
	})

	t.Run("GetClientCertificate_always_sends_cert", func(t *testing.T) {
		srv, sawCert := startServer(t)
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{
				GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
					return &cert, nil
				},
				RootCAs:    serverPool,
				MinVersion: tls.VersionTLS12,
			},
		}
		client := &http.Client{Transport: tr}
		resp, err := client.Get(srv.URL)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.True(t, sawCert.Load(),
			"GetClientCertificate should always send the cert, even leaf-only")
	})
}
