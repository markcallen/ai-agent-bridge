package pki

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CrossSign takes a target CA certificate and re-signs it using the signer CA,
// creating a cross-signed certificate that chains to the signer's trust root.
func CrossSign(signerCert *x509.Certificate, signerKey *ecdsa.PrivateKey, targetCert *x509.Certificate, outPath string) error {
	serial, err := randomSerial()
	if err != nil {
		return err
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               targetCert.Subject,
		NotBefore:             now,
		NotAfter:              targetCert.NotAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, signerCert, targetCert.PublicKey, signerKey)
	if err != nil {
		return fmt.Errorf("cross-sign cert: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	return writePEM(outPath, "CERTIFICATE", certDER, 0o644)
}
