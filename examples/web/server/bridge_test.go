package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/markcallen/ai-agent-bridge/internal/pki"
)

func TestDefaultServerName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		want   string
	}{
		{name: "compose alias", target: "bridge.local:9445", want: "bridge.local"},
		{name: "compose service", target: "bridge:9445", want: "bridge.local"},
		{name: "localhost", target: "localhost:9445", want: "bridge.local"},
		{name: "ip", target: "127.0.0.1:9445", want: "bridge.local"},
		{name: "remote host", target: "machine.tailnet.ts.net:9445", want: "machine.tailnet.ts.net"},
		{name: "host without port", target: "machine.tailnet.ts.net", want: "machine.tailnet.ts.net"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := defaultServerName(tt.target); got != tt.want {
				t.Fatalf("defaultServerName(%q)=%q want %q", tt.target, got, tt.want)
			}
		})
	}
}

func TestEnvBridgeClientRequiresCredentialEnv(t *testing.T) {
	t.Setenv("BRIDGE_ADDR", "bridge.local:9445")
	t.Setenv("CA_CERT", "")
	t.Setenv("CLIENT_CERT", "")
	t.Setenv("CLIENT_KEY", "")
	t.Setenv("JWT_KEY", "")

	client, err := envBridgeClient(time.Second)
	if err == nil {
		if client != nil {
			_ = client.Close()
		}
		t.Fatal("envBridgeClient succeeded with missing credential env")
	}
	if !strings.Contains(err.Error(), "BRIDGE_ADDR requires") {
		t.Fatalf("envBridgeClient error=%q, want credential hint", err)
	}
}

func TestEnvBridgeClientUsesCredentialEnv(t *testing.T) {
	certsDir := t.TempDir()

	caCertPath, caKeyPath, err := pki.InitCA("test-ca", certsDir)
	if err != nil {
		t.Fatalf("init ca: %v", err)
	}
	caCert, caKey, err := pki.LoadCA(caCertPath, caKeyPath)
	if err != nil {
		t.Fatalf("load ca: %v", err)
	}
	clientCertPath, clientKeyPath, err := pki.IssueCert(caCert, caKey, pki.CertTypeClient, "dev-client", nil, certsDir, time.Hour)
	if err != nil {
		t.Fatalf("issue client cert: %v", err)
	}
	_, jwtKeyPath, err := pki.GenerateJWTKeypair(certsDir, "jwt-signing")
	if err != nil {
		t.Fatalf("generate jwt keypair: %v", err)
	}

	caBundlePath := filepath.Join(certsDir, "ca-bundle.crt")
	caBundle, err := os.ReadFile(caCertPath)
	if err != nil {
		t.Fatalf("read ca cert: %v", err)
	}
	if err := os.WriteFile(caBundlePath, caBundle, 0o644); err != nil {
		t.Fatalf("write ca bundle: %v", err)
	}

	t.Setenv("BRIDGE_ADDR", "bridge.local")
	t.Setenv("CA_CERT", caBundlePath)
	t.Setenv("CLIENT_CERT", clientCertPath)
	t.Setenv("CLIENT_KEY", clientKeyPath)
	t.Setenv("JWT_KEY", jwtKeyPath)
	t.Setenv("JWT_ISSUER", "dev")

	client, err := envBridgeClient(time.Second)
	if err != nil {
		t.Fatalf("envBridgeClient: %v", err)
	}
	defer func() { _ = client.Close() }()
}
