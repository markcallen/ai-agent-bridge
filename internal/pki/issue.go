package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CertType indicates whether to issue a server or client certificate.
type CertType int

const (
	CertTypeServer CertType = iota
	CertTypeClient
)

// IssueCert generates a new ECDSA P-384 keypair and certificate signed by the given CA.
func IssueCert(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, ct CertType, cn string, sans []string, outDir string) (certPath, keyPath string, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return "", "", err
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: cn,
		},
		NotBefore: now,
		NotAfter:  now.AddDate(0, 0, certValidityDays),
	}

	switch ct {
	case CertTypeServer:
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment
	case CertTypeClient:
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	}

	for _, san := range sans {
		san = strings.TrimSpace(san)
		if san == "" {
			continue
		}
		if ip := net.ParseIP(san); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, san)
		}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &priv.PublicKey, caKey)
	if err != nil {
		return "", "", fmt.Errorf("create cert: %w", err)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir %s: %w", outDir, err)
	}

	baseName := strings.ReplaceAll(cn, " ", "-")
	certPath = filepath.Join(outDir, baseName+".crt")
	keyPath = filepath.Join(outDir, baseName+".key")

	if err := writePEM(certPath, "CERTIFICATE", certDER, 0o644); err != nil {
		return "", "", err
	}

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return "", "", fmt.Errorf("marshal key: %w", err)
	}
	if err := writePEM(keyPath, "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return "", "", err
	}

	return certPath, keyPath, nil
}
