package server

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/internal/auth"
	"github.com/markcallen/ai-agent-bridge/internal/pki"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var safeIssuerRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func (s *BridgeServer) RegisterJWTKey(ctx context.Context, req *bridgev1.RegisterJWTKeyRequest) (*bridgev1.RegisterJWTKeyResponse, error) {
	if !s.globalRL.allow("global") {
		return nil, status.Error(codes.ResourceExhausted, "global RPC rate limit exceeded")
	}

	// mTLS CN is the authorization — only clients with a valid cert can enroll.
	cn := auth.CallerCommonName(ctx)
	if cn == "" {
		return nil, status.Error(codes.Unauthenticated, "valid mTLS client certificate required for enrollment")
	}

	issuer := req.Issuer
	if issuer == "" {
		issuer = cn
	}

	if !safeIssuerRe.MatchString(issuer) {
		return nil, status.Error(codes.InvalidArgument, "issuer must be alphanumeric with hyphens, underscores, or dots")
	}

	// Callers may only register their own issuer slot (issuer must equal the
	// mTLS CN). This prevents one client from overwriting another client's JWT
	// key by supplying a foreign or reserved issuer name.
	if issuer != cn {
		return nil, status.Errorf(codes.PermissionDenied, "issuer %q does not match client certificate CN %q", issuer, cn)
	}

	if len(req.PublicKey) == 0 {
		return nil, status.Error(codes.InvalidArgument, "public_key is required")
	}

	pubKey, err := x509.ParsePKIXPublicKey(req.PublicKey)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid public key: %v", err)
	}
	edKey, ok := pubKey.(ed25519.PublicKey)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "public key must be Ed25519")
	}

	if s.jwtVerifier == nil {
		return nil, status.Error(codes.FailedPrecondition, "JWT enrollment not available in local mode")
	}

	// Persist to disk so the key survives server restarts.
	if s.certsDir != "" {
		jwtClientsDir := filepath.Join(s.certsDir, "jwt-clients")
		if err := os.MkdirAll(jwtClientsDir, 0o700); err != nil {
			return nil, status.Errorf(codes.Internal, "create jwt-clients dir: %v", err)
		}
		pubPath := filepath.Join(jwtClientsDir, issuer+".pub")
		if err := pki.WritePublicKeyPEM(pubPath, edKey); err != nil {
			return nil, status.Errorf(codes.Internal, "write public key: %v", err)
		}
	}

	// Hot-add to the live verifier — no server restart needed.
	s.jwtVerifier.AddKey(issuer, edKey)

	s.logger.Info("registered JWT key", "issuer", issuer, "caller_cn", cn)

	return &bridgev1.RegisterJWTKeyResponse{
		Issuer:  issuer,
		Message: fmt.Sprintf("JWT public key registered for issuer %q", issuer),
	}, nil
}
