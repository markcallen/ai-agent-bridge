package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestJWTMintAndVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	issuer := &JWTIssuer{
		Issuer:   "test-issuer",
		Audience: "bridge",
		Key:      priv,
		TTL:      5 * time.Minute,
	}

	token, err := issuer.Mint("user-1", "project-abc")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	verifier := &JWTVerifier{
		Audience: "bridge",
		MaxTTL:   10 * time.Minute,
		Keys: map[string]ed25519.PublicKey{
			"test-issuer": pub,
		},
	}

	claims, err := verifier.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if claims.ProjectID != "project-abc" {
		t.Errorf("ProjectID = %q, want %q", claims.ProjectID, "project-abc")
	}
	if claims.Subject != "user-1" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "user-1")
	}
}

func TestJWTWrongIssuer(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)

	issuer := &JWTIssuer{
		Issuer:   "evil-issuer",
		Audience: "bridge",
		Key:      priv,
		TTL:      5 * time.Minute,
	}
	token, _ := issuer.Mint("user-1", "project-abc")

	verifier := &JWTVerifier{
		Audience: "bridge",
		Keys: map[string]ed25519.PublicKey{
			"good-issuer": pub2,
		},
	}

	_, err := verifier.Verify(token)
	if err == nil {
		t.Error("expected error for unknown issuer")
	}
}

func TestJWTExceedsMaxTTL(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	issuer := &JWTIssuer{
		Issuer:   "test",
		Audience: "bridge",
		Key:      priv,
		TTL:      1 * time.Hour,
	}
	token, _ := issuer.Mint("user-1", "project-abc")

	verifier := &JWTVerifier{
		Audience: "bridge",
		MaxTTL:   5 * time.Minute,
		Keys:     map[string]ed25519.PublicKey{"test": pub},
	}

	_, err := verifier.Verify(token)
	if err == nil {
		t.Error("expected error for TTL exceeding max")
	}
}

func TestJWTWrongAudience(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	issuer := &JWTIssuer{
		Issuer:   "test",
		Audience: "wrong-aud",
		Key:      priv,
		TTL:      5 * time.Minute,
	}
	token, _ := issuer.Mint("user-1", "project-abc")

	verifier := &JWTVerifier{
		Audience: "bridge",
		Keys:     map[string]ed25519.PublicKey{"test": pub},
	}

	_, err := verifier.Verify(token)
	if err == nil {
		t.Error("expected error for wrong audience")
	}
}
