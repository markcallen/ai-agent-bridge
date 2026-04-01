package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"log/slog"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/markcallen/ai-agent-bridge/internal/pki"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type testServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *testServerStream) Context() context.Context { return s.ctx }

func TestJWTInterceptorsAndHelpers(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	issuer := &JWTIssuer{
		Issuer:   "issuer-a",
		Audience: "bridge",
		Key:      priv,
		TTL:      time.Minute,
	}
	token, err := issuer.Mint("user-a", "project-a")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	verifier := &JWTVerifier{
		Audience: "bridge",
		MaxTTL:   time.Minute,
		Keys:     map[string]ed25519.PublicKey{"issuer-a": pub},
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	claims, err := extractAndVerify(ctx, verifier)
	if err != nil {
		t.Fatalf("extractAndVerify: %v", err)
	}
	if claims.Subject != "user-a" || claims.ProjectID != "project-a" {
		t.Fatalf("claims=%+v", claims)
	}

	unary := UnaryJWTInterceptor(verifier, nil)
	resp, err := unary(ctx, "req", &grpc.UnaryServerInfo{FullMethod: "/bridge.v1.BridgeService/ListProviders"}, func(callCtx context.Context, req any) (any, error) {
		claims, ok := ClaimsFromContext(callCtx)
		if !ok || claims.Subject != "user-a" {
			t.Fatalf("ClaimsFromContext ok=%v claims=%+v", ok, claims)
		}
		return "ok", nil
	})
	if err != nil || resp != "ok" {
		t.Fatalf("UnaryJWTInterceptor resp=%v err=%v", resp, err)
	}

	_, err = unary(context.Background(), "req", &grpc.UnaryServerInfo{FullMethod: "/bridge.v1.BridgeService/ListProviders"}, func(context.Context, any) (any, error) {
		return nil, nil
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated code=%v want %v", status.Code(err), codes.Unauthenticated)
	}

	stream := StreamJWTInterceptor(verifier, nil)
	err = stream(nil, &testServerStream{ctx: ctx}, &grpc.StreamServerInfo{FullMethod: "/bridge.v1.BridgeService/AttachSession"}, func(srv any, ss grpc.ServerStream) error {
		claims, ok := ClaimsFromContext(ss.Context())
		if !ok || claims.ProjectID != "project-a" {
			t.Fatalf("stream claims ok=%v claims=%+v", ok, claims)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StreamJWTInterceptor: %v", err)
	}

	if got, err := parseBearerToken("Bearer token-value"); err != nil || got != "token-value" {
		t.Fatalf("parseBearerToken got=%q err=%v", got, err)
	}
	if _, err := parseBearerToken("token-value"); err == nil {
		t.Fatal("parseBearerToken accepted invalid header")
	}
}

func TestPassthroughAndCallerCommonName(t *testing.T) {
	unary := UnaryPassthroughInterceptor()
	_, err := unary(context.Background(), "req", &grpc.UnaryServerInfo{FullMethod: "/bridge.v1.BridgeService/ListProviders"}, func(ctx context.Context, req any) (any, error) {
		claims, ok := ClaimsFromContext(ctx)
		if !ok || claims == nil {
			t.Fatalf("ClaimsFromContext ok=%v claims=%+v", ok, claims)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("UnaryPassthroughInterceptor: %v", err)
	}

	stream := StreamPassthroughInterceptor()
	err = stream(nil, &testServerStream{ctx: context.Background()}, &grpc.StreamServerInfo{FullMethod: "/bridge.v1.BridgeService/AttachSession"}, func(srv any, ss grpc.ServerStream) error {
		claims, ok := ClaimsFromContext(ss.Context())
		if !ok || claims == nil {
			t.Fatalf("ClaimsFromContext ok=%v claims=%+v", ok, claims)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StreamPassthroughInterceptor: %v", err)
	}

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "client-a"},
	}
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.IPAddr{},
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}},
		},
	})
	if got := callerCommonName(ctx); got != "client-a" {
		t.Fatalf("callerCommonName=%q want %q", got, "client-a")
	}
	if got := requestStringField(struct{ ProjectId string }{ProjectId: "project-a"}, "ProjectId"); got != "project-a" {
		t.Fatalf("requestStringField=%q want %q", got, "project-a")
	}
}

func TestAuditInterceptorsAndTLSConfig(t *testing.T) {
	logger := slogDiscardLogger()
	ctx := authContextWithClaims(context.Background(), "project-a", "user-a")

	unary := UnaryAuditInterceptor(logger)
	_, err := unary(ctx, struct {
		ProjectId string
		SessionId string
	}{ProjectId: "project-a", SessionId: "session-a"}, &grpc.UnaryServerInfo{FullMethod: "/bridge.v1.BridgeService/GetSession"}, func(context.Context, any) (any, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("UnaryAuditInterceptor success: %v", err)
	}

	_, err = unary(ctx, struct{ SessionId string }{SessionId: "session-a"}, &grpc.UnaryServerInfo{FullMethod: "/bridge.v1.BridgeService/GetSession"}, func(context.Context, any) (any, error) {
		return nil, status.Error(codes.NotFound, "missing")
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("UnaryAuditInterceptor error code=%v want %v", status.Code(err), codes.NotFound)
	}

	stream := StreamAuditInterceptor(logger)
	if err := stream(nil, &testServerStream{ctx: ctx}, &grpc.StreamServerInfo{FullMethod: "/bridge.v1.BridgeService/AttachSession"}, func(any, grpc.ServerStream) error {
		return nil
	}); err != nil {
		t.Fatalf("StreamAuditInterceptor success: %v", err)
	}

	dir := t.TempDir()
	caCert, caKey, err := pki.InitCA("test-ca", dir)
	if err != nil {
		t.Fatalf("InitCA: %v", err)
	}
	serverCert, serverKey, err := pki.IssueCert(mustLoadCA(t, caCert, caKey), mustLoadCAKey(t, caCert, caKey), pki.CertTypeServer, "bridge.local", []string{"bridge.local", "127.0.0.1"}, dir)
	if err != nil {
		t.Fatalf("Issue server cert: %v", err)
	}
	clientCert, clientKey, err := pki.IssueCert(mustLoadCA(t, caCert, caKey), mustLoadCAKey(t, caCert, caKey), pki.CertTypeClient, "client-a", nil, dir)
	if err != nil {
		t.Fatalf("Issue client cert: %v", err)
	}
	bundle := filepath.Join(dir, "bundle.crt")
	if err := pki.BuildBundle(bundle, caCert); err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}

	serverTLS, err := ServerTLSConfig(TLSConfig{
		CABundlePath: bundle,
		CertPath:     serverCert,
		KeyPath:      serverKey,
	})
	if err != nil {
		t.Fatalf("ServerTLSConfig: %v", err)
	}
	if serverTLS.MinVersion != tls.VersionTLS13 {
		t.Fatalf("server MinVersion=%v want TLS1.3", serverTLS.MinVersion)
	}

	clientTLS, err := ClientTLSConfig(TLSConfig{
		CABundlePath: bundle,
		CertPath:     clientCert,
		KeyPath:      clientKey,
		ServerName:   "bridge.local",
	})
	if err != nil {
		t.Fatalf("ClientTLSConfig: %v", err)
	}
	if clientTLS.ServerName != "bridge.local" {
		t.Fatalf("client ServerName=%q want %q", clientTLS.ServerName, "bridge.local")
	}

	if _, err := loadCAPool(filepath.Join(dir, "missing.crt")); err == nil {
		t.Fatal("loadCAPool accepted a missing file")
	}
	invalidBundle := filepath.Join(dir, "invalid.crt")
	if err := os.WriteFile(invalidBundle, []byte("not pem"), 0o644); err != nil {
		t.Fatalf("WriteFile invalid bundle: %v", err)
	}
	if _, err := loadCAPool(invalidBundle); err == nil {
		t.Fatal("loadCAPool accepted invalid PEM")
	}
}

func authContextWithClaims(ctx context.Context, projectID, subject string) context.Context {
	return ContextWithClaims(ctx, &BridgeClaims{
		ProjectID: projectID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: subject,
		},
	})
}

func slogDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustLoadCA(t *testing.T, certPath, keyPath string) *x509.Certificate {
	t.Helper()
	cert, _, err := pki.LoadCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadCA cert: %v", err)
	}
	return cert
}

func mustLoadCAKey(t *testing.T, certPath, keyPath string) *ecdsa.PrivateKey {
	t.Helper()
	_, key, err := pki.LoadCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadCA key: %v", err)
	}
	return key
}
