// Package enrollment implements one-time enrollment tokens used to
// bootstrap client identities against a bridgectl server.
package enrollment

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	// TokenPrefix is prepended to every enrollment token for visual identification.
	TokenPrefix = "brg_enroll_"
	// DefaultTokenExpiry is the default lifetime of an enrollment token.
	DefaultTokenExpiry = 15 * time.Minute
	// tokenRandomBytes is the number of random bytes in a token.
	tokenRandomBytes = 24
)

// Token represents a one-time enrollment credential.
type Token struct {
	// Value is the full token string including prefix (e.g. "brg_enroll_abc123...").
	Value string `json:"value"`
	// Identity is the intended certificate CN for the enrolling client.
	Identity string `json:"identity"`
	// ExpiresAt is when this token becomes invalid.
	ExpiresAt time.Time `json:"expires_at"`
	// Used indicates the token has been consumed by a successful enrollment.
	Used bool `json:"used"`
	// CreatedAt is when the token was generated.
	CreatedAt time.Time `json:"created_at"`
	// CreatedBy is the identity that generated this token (e.g. admin CN).
	CreatedBy string `json:"created_by,omitempty"`
	// ServerURL is the bridgectl server URL encoded in the token metadata.
	ServerURL string `json:"server_url,omitempty"`
	// CAFingerprint is the SHA-256 fingerprint of the CA root for trust bootstrap.
	CAFingerprint string `json:"ca_fingerprint,omitempty"`
}

// Generate creates a new enrollment token for the given identity.
func Generate(identity string, expiry time.Duration, serverURL, caFingerprint string) (*Token, error) {
	if identity == "" {
		return nil, fmt.Errorf("enrollment: identity is required")
	}
	if expiry <= 0 {
		expiry = DefaultTokenExpiry
	}

	raw := make([]byte, tokenRandomBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("enrollment: generate random: %w", err)
	}

	now := time.Now()
	return &Token{
		Value:         TokenPrefix + hex.EncodeToString(raw),
		Identity:      identity,
		ExpiresAt:     now.Add(expiry),
		CreatedAt:     now,
		ServerURL:     serverURL,
		CAFingerprint: caFingerprint,
	}, nil
}

// IsExpired reports whether the token has passed its expiry time.
func (t *Token) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}

// IsValid reports whether the token can still be used for enrollment.
func (t *Token) IsValid() bool {
	return !t.Used && !t.IsExpired()
}

// MarkUsed marks the token as consumed. It returns an error if the token
// is already used or expired.
func (t *Token) MarkUsed() error {
	if t.Used {
		return fmt.Errorf("enrollment: token already used")
	}
	if t.IsExpired() {
		return fmt.Errorf("enrollment: token expired at %s", t.ExpiresAt.Format(time.RFC3339))
	}
	t.Used = true
	return nil
}

// Redacted returns the token value with the random portion masked for
// safe logging. Only the first 8 hex characters are visible.
// Safe to call on zero-value tokens or tokens with short values.
func (t *Token) Redacted() string {
	if t == nil || t.Value == "" {
		return "[no token]"
	}
	prefixLen := len(TokenPrefix)
	if len(t.Value) <= prefixLen {
		return t.Value + "***"
	}
	showLen := prefixLen + 8
	if showLen > len(t.Value) {
		showLen = len(t.Value)
	}
	return t.Value[:showLen] + "***"
}
