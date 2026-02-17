package auth

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

// BridgeClaims are the JWT claims required for bridge API access.
type BridgeClaims struct {
	ProjectID string `json:"project_id"`
	jwt.RegisteredClaims
}

// JWTIssuer mints Ed25519-signed JWTs for bridge authentication.
type JWTIssuer struct {
	Issuer   string
	Audience string
	Key      ed25519.PrivateKey
	TTL      time.Duration
}

// Mint creates a new JWT with the given subject and project ID.
func (j *JWTIssuer) Mint(sub, projectID string) (string, error) {
	now := time.Now()
	claims := BridgeClaims{
		ProjectID: projectID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    j.Issuer,
			Subject:   sub,
			Audience:  jwt.ClaimStrings{j.Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(j.TTL)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	return tok.SignedString(j.Key)
}

// JWTVerifier verifies Ed25519-signed JWTs from multiple issuers.
type JWTVerifier struct {
	Audience string
	MaxTTL   time.Duration
	// Keys maps issuer name to their Ed25519 public key.
	Keys map[string]ed25519.PublicKey
}

// Verify parses and validates a JWT token string.
func (v *JWTVerifier) Verify(tokenString string) (*BridgeClaims, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"EdDSA"}),
		jwt.WithAudience(v.Audience),
	)

	claims := &BridgeClaims{}
	_, err := parser.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		issuer, err := claims.GetIssuer()
		if err != nil || issuer == "" {
			return nil, errors.New("missing issuer")
		}
		key, ok := v.Keys[issuer]
		if !ok {
			return nil, fmt.Errorf("unknown issuer: %s", issuer)
		}
		return key, nil
	})
	if err != nil {
		return nil, fmt.Errorf("verify jwt: %w", err)
	}

	// Enforce max TTL
	if v.MaxTTL > 0 {
		iat, err := claims.GetIssuedAt()
		if err != nil || iat == nil {
			return nil, errors.New("missing iat claim")
		}
		exp, err := claims.GetExpirationTime()
		if err != nil || exp == nil {
			return nil, errors.New("missing exp claim")
		}
		if exp.Sub(iat.Time) > v.MaxTTL {
			return nil, fmt.Errorf("token TTL %s exceeds max %s", exp.Sub(iat.Time), v.MaxTTL)
		}
	}

	return claims, nil
}
