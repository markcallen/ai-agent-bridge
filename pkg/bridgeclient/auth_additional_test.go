package bridgeclient

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/markcallen/ai-agent-bridge/internal/pki"
)

func TestJWTCredentialsAndTransportTLS(t *testing.T) {
	dir := t.TempDir()
	_, keyPath, err := pki.GenerateJWTKeypair(dir, "jwt")
	if err != nil {
		t.Fatalf("GenerateJWTKeypair: %v", err)
	}

	creds, err := newJWTCredentials(&JWTConfig{
		PrivateKeyPath: keyPath,
		Issuer:         "issuer-a",
		Audience:       "bridge",
	})
	if err != nil {
		t.Fatalf("newJWTCredentials: %v", err)
	}
	creds.SetProject("project-a")
	md, err := creds.GetRequestMetadata(context.Background())
	if err != nil {
		t.Fatalf("GetRequestMetadata: %v", err)
	}
	if md["authorization"] == "" {
		t.Fatal("authorization metadata was empty")
	}
	if creds.RequireTransportSecurity() {
		t.Fatal("RequireTransportSecurity returned true")
	}

	caCert, caKey, err := pki.InitCA("bridge-ca", dir)
	if err != nil {
		t.Fatalf("InitCA: %v", err)
	}
	ca, key, err := pki.LoadCA(caCert, caKey)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}
	clientCert, clientKey, err := pki.IssueCert(ca, key, pki.CertTypeClient, "client-a", nil, dir)
	if err != nil {
		t.Fatalf("IssueCert client: %v", err)
	}
	bundle := filepath.Join(dir, "bundle.crt")
	if err := pki.BuildBundle(bundle, caCert); err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}

	tlsCreds, err := buildTransportCredentials(&MTLSConfig{
		CABundlePath: bundle,
		CertPath:     clientCert,
		KeyPath:      clientKey,
		ServerName:   "bridge.local",
	})
	if err != nil {
		t.Fatalf("buildTransportCredentials: %v", err)
	}
	if tlsCreds.Info().SecurityProtocol == "" {
		t.Fatalf("tls credentials info=%+v", tlsCreds.Info())
	}

	client, err := New(
		WithTarget("127.0.0.1:9445"),
		WithMTLS(MTLSConfig{
			CABundlePath: bundle,
			CertPath:     clientCert,
			KeyPath:      clientKey,
			ServerName:   "bridge.local",
		}),
		WithJWT(JWTConfig{
			PrivateKeyPath: keyPath,
			Issuer:         "issuer-a",
			Audience:       "bridge",
		}),
		WithTimeout(time.Second),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.SetProject("project-a")
	if client.jwtCred == nil {
		t.Fatal("jwtCred was nil")
	}
}
