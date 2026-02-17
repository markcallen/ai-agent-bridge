package bridgeclient

import (
	"context"
	"sync"
	"time"

	"github.com/markcallen/ai-agent-bridge/internal/auth"
	"github.com/markcallen/ai-agent-bridge/internal/pki"
	"google.golang.org/grpc/credentials"
)

// jwtCredentials implements grpc.PerRPCCredentials with auto-renewal.
type jwtCredentials struct {
	issuer *auth.JWTIssuer

	mu        sync.Mutex
	token     string
	expiresAt time.Time
	projectID string
	subject   string
}

func newJWTCredentials(cfg *JWTConfig) (*jwtCredentials, error) {
	privKey, err := pki.LoadEd25519PrivateKey(cfg.PrivateKeyPath)
	if err != nil {
		return nil, err
	}

	ttl := cfg.TTL
	if ttl == 0 {
		ttl = 5 * time.Minute
	}

	return &jwtCredentials{
		issuer: &auth.JWTIssuer{
			Issuer:   cfg.Issuer,
			Audience: cfg.Audience,
			Key:      privKey,
			TTL:      ttl,
		},
		subject: cfg.Issuer, // default subject = issuer
	}, nil
}

// SetProject sets the project_id to include in minted tokens.
func (j *jwtCredentials) SetProject(projectID string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.projectID != projectID {
		j.projectID = projectID
		j.token = "" // force re-mint
	}
}

func (j *jwtCredentials) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	// Auto-renew if expired or within 30s of expiry
	if j.token == "" || time.Now().After(j.expiresAt.Add(-30*time.Second)) {
		tok, err := j.issuer.Mint(j.subject, j.projectID)
		if err != nil {
			return nil, err
		}
		j.token = tok
		j.expiresAt = time.Now().Add(j.issuer.TTL)
	}

	return map[string]string{
		"authorization": "Bearer " + j.token,
	}, nil
}

func (j *jwtCredentials) RequireTransportSecurity() bool {
	return false // Allow insecure for dev; mTLS handles transport security
}

// buildTransportCredentials creates gRPC transport credentials from mTLS config.
func buildTransportCredentials(cfg *MTLSConfig) (credentials.TransportCredentials, error) {
	tlsCfg, err := auth.ClientTLSConfig(auth.TLSConfig{
		CABundlePath: cfg.CABundlePath,
		CertPath:     cfg.CertPath,
		KeyPath:      cfg.KeyPath,
		ServerName:   cfg.ServerName,
	})
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(tlsCfg), nil
}
