package pki

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

// GenerateJWTKeypair creates an Ed25519 keypair for JWT signing.
func GenerateJWTKeypair(outDir, baseName string) (pubPath, privPath string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate ed25519 key: %w", err)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir: %w", err)
	}

	privPath = filepath.Join(outDir, baseName+".key")
	pubPath = filepath.Join(outDir, baseName+".pub")

	// Marshal private key to PKCS8
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", fmt.Errorf("marshal private key: %w", err)
	}
	if err := writePEM(privPath, "PRIVATE KEY", privDER, 0o600); err != nil {
		return "", "", err
	}

	// Marshal public key
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", "", fmt.Errorf("marshal public key: %w", err)
	}
	if err := writePEM(pubPath, "PUBLIC KEY", pubDER, 0o644); err != nil {
		return "", "", err
	}

	return pubPath, privPath, nil
}

// LoadEd25519PrivateKey loads an Ed25519 private key from a PEM file.
func LoadEd25519PrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("decode pem: no block found")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse key: %w", err)
	}
	edKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not ed25519")
	}
	return edKey, nil
}

// LoadEd25519PublicKey loads an Ed25519 public key from a PEM file.
func LoadEd25519PublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("decode pem: no block found")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse key: %w", err)
	}
	edKey, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key is not ed25519")
	}
	return edKey, nil
}
