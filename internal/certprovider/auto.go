package certprovider

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"fmt"
	"path/filepath"

	"github.com/orchael/bridgectl/internal/pki"
)

// AutoProvider implements CertificateProvider using a locally generated
// self-signed CA. It wraps the existing pki.InitCA and pki.IssueCert
// functions. This provider is used when no external PKI is configured.
type AutoProvider struct {
	// CertsDir is the directory where CA and issued certs are stored.
	CertsDir string
	// CAName is the Common Name for the auto-generated CA certificate.
	CAName string
}

// NewAutoProvider creates an AutoProvider that stores certificate material
// in certsDir. The CA is created lazily on the first Enroll call if it
// does not already exist.
func NewAutoProvider(certsDir, caName string) (*AutoProvider, error) {
	if certsDir == "" {
		return nil, fmt.Errorf("auto provider: certs directory is required")
	}
	if caName == "" {
		caName = "bridgectl-auto-ca"
	}
	return &AutoProvider{CertsDir: certsDir, CAName: caName}, nil
}

// Enroll generates a certificate signed by the auto-generated CA.
// If the CA does not exist yet, it is created first.
func (p *AutoProvider) Enroll(_ context.Context, req EnrollmentRequest) (*Identity, error) {
	caCert, caKey, err := p.ensureCA()
	if err != nil {
		return nil, fmt.Errorf("auto enroll: %w", err)
	}

	ct := pki.CertTypeClient
	if req.Role == RoleServer {
		ct = pki.CertTypeServer
	}

	outDir := req.OutDir
	if outDir == "" {
		outDir = p.CertsDir
	}

	certPath, keyPath, err := pki.IssueCert(caCert, caKey, ct, req.CommonName, req.SANs, outDir, req.Validity)
	if err != nil {
		return nil, fmt.Errorf("auto enroll: issue cert: %w", err)
	}

	cert, err := pki.LoadCert(certPath)
	if err != nil {
		return nil, fmt.Errorf("auto enroll: load issued cert: %w", err)
	}

	return &Identity{
		CertPath:   certPath,
		KeyPath:    keyPath,
		CACertPath: filepath.Join(p.CertsDir, "ca.crt"),
		Provider:   "auto",
		CommonName: cert.Subject.CommonName,
		NotBefore:  cert.NotBefore,
		NotAfter:   cert.NotAfter,
		Renewable:  true,
	}, nil
}

// Renew re-issues a certificate from the local CA with the same CN and
// SANs but a fresh validity period.
func (p *AutoProvider) Renew(_ context.Context, identity *Identity) error {
	caCert, caKey, err := p.ensureCA()
	if err != nil {
		return fmt.Errorf("auto renew: %w", err)
	}

	// Load the existing cert to extract CN and SANs.
	existing, err := pki.LoadCert(identity.CertPath)
	if err != nil {
		return fmt.Errorf("auto renew: load existing cert: %w", err)
	}

	ct := pki.CertTypeClient
	for _, eku := range existing.ExtKeyUsage {
		if eku == x509.ExtKeyUsageServerAuth {
			ct = pki.CertTypeServer
			break
		}
	}

	// Combine DNS names and IP addresses back into SANs.
	sans := append([]string{}, existing.DNSNames...)
	for _, ip := range existing.IPAddresses {
		sans = append(sans, ip.String())
	}

	validity := existing.NotAfter.Sub(existing.NotBefore)

	outDir := filepath.Dir(identity.CertPath)
	certPath, keyPath, err := pki.IssueCert(caCert, caKey, ct, existing.Subject.CommonName, sans, outDir, validity)
	if err != nil {
		return fmt.Errorf("auto renew: issue cert: %w", err)
	}

	// Update the identity with the new paths and validity.
	renewed, err := pki.LoadCert(certPath)
	if err != nil {
		return fmt.Errorf("auto renew: load renewed cert: %w", err)
	}

	identity.CertPath = certPath
	identity.KeyPath = keyPath
	identity.NotBefore = renewed.NotBefore
	identity.NotAfter = renewed.NotAfter

	return nil
}

// Roots returns the auto-generated CA certificate.
func (p *AutoProvider) Roots(_ context.Context) ([]*x509.Certificate, error) {
	caPath := filepath.Join(p.CertsDir, "ca.crt")
	return loadCertBundle(caPath)
}

// ensureCA creates the CA if it doesn't exist, or loads it if it does.
func (p *AutoProvider) ensureCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	caPath := filepath.Join(p.CertsDir, "ca.crt")
	keyPath := filepath.Join(p.CertsDir, "ca.key")

	// Try to load existing CA first.
	cert, key, err := pki.LoadCA(caPath, keyPath)
	if err == nil {
		return cert, key, nil
	}

	// Generate a new CA.
	_, _, err = pki.InitCA(p.CAName, p.CertsDir)
	if err != nil {
		return nil, nil, fmt.Errorf("init ca: %w", err)
	}

	return pki.LoadCA(caPath, keyPath)
}

// CAKeyPath returns the path to the CA private key. This is needed by
// callers that manage cross-signing or other CA-level operations.
func (p *AutoProvider) CAKeyPath() string {
	return filepath.Join(p.CertsDir, "ca.key")
}

// CACertPath returns the path to the CA certificate.
func (p *AutoProvider) CACertPath() string {
	return filepath.Join(p.CertsDir, "ca.crt")
}

// Compile-time interface check.
var _ CertificateProvider = (*AutoProvider)(nil)
