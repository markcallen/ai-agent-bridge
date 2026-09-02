package certprovider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
)

// FilesystemConfig holds paths to PEM-encoded certificate material on disk.
type FilesystemConfig struct {
	// CACertPath is the path to the CA certificate or bundle.
	CACertPath string
	// CertPath is the path to the end-entity certificate.
	CertPath string
	// KeyPath is the path to the private key matching CertPath.
	KeyPath string
}

// FilesystemProvider implements CertificateProvider by reading
// pre-existing certificate material from the local filesystem.
// It does not issue or renew certificates — that responsibility
// belongs to an external PKI.
type FilesystemProvider struct {
	cfg FilesystemConfig
}

// NewFilesystemProvider creates a FilesystemProvider and validates that the
// configured certificate material is present, well-formed, and consistent.
func NewFilesystemProvider(cfg FilesystemConfig) (*FilesystemProvider, error) {
	if cfg.CACertPath == "" {
		return nil, fmt.Errorf("filesystem provider: ca cert path is required")
	}
	if cfg.CertPath == "" {
		return nil, fmt.Errorf("filesystem provider: certificate path is required")
	}
	if cfg.KeyPath == "" {
		return nil, fmt.Errorf("filesystem provider: private key path is required")
	}

	// Validate that the files exist and the cert/key pair is consistent.
	if err := validateFilesystemMaterial(cfg); err != nil {
		return nil, fmt.Errorf("filesystem provider: %w", err)
	}

	return &FilesystemProvider{cfg: cfg}, nil
}

// Enroll returns the pre-configured identity. The filesystem provider
// does not perform actual enrollment — certificates are managed
// externally.
func (p *FilesystemProvider) Enroll(_ context.Context, req EnrollmentRequest) (*Identity, error) {
	cert, err := loadFirstCert(p.cfg.CertPath)
	if err != nil {
		return nil, fmt.Errorf("filesystem enroll: %w", err)
	}

	return &Identity{
		CertPath:   p.cfg.CertPath,
		KeyPath:    p.cfg.KeyPath,
		CACertPath: p.cfg.CACertPath,
		Provider:   "filesystem",
		CommonName: cert.Subject.CommonName,
		NotBefore:  cert.NotBefore,
		NotAfter:   cert.NotAfter,
		Renewable:  false,
	}, nil
}

// Renew returns ErrRenewalNotSupported. Filesystem certificates are
// managed by an external process.
func (p *FilesystemProvider) Renew(_ context.Context, _ *Identity) error {
	return ErrRenewalNotSupported
}

// Roots parses and returns the CA certificates from the configured
// CA bundle file.
func (p *FilesystemProvider) Roots(_ context.Context) ([]*x509.Certificate, error) {
	return loadCertBundle(p.cfg.CACertPath)
}

// validateFilesystemMaterial checks that the certificate material is
// present, parseable, and consistent.
func validateFilesystemMaterial(cfg FilesystemConfig) error {
	// Verify the CA bundle is readable and contains at least one cert.
	caCerts, err := loadCertBundle(cfg.CACertPath)
	if err != nil {
		return fmt.Errorf("load ca bundle: %w", err)
	}
	if len(caCerts) == 0 {
		return fmt.Errorf("ca bundle %s contains no certificates", cfg.CACertPath)
	}

	// Verify the cert/key pair loads as a TLS keypair.
	_, err = tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
	if err != nil {
		return fmt.Errorf("cert/key mismatch or unreadable: %w", err)
	}

	// Verify the certificate chains to the CA bundle.
	cert, err := loadFirstCert(cfg.CertPath)
	if err != nil {
		return fmt.Errorf("load certificate: %w", err)
	}

	pool := x509.NewCertPool()
	for _, ca := range caCerts {
		pool.AddCert(ca)
	}
	verifyOpts := x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}
	if _, err := cert.Verify(verifyOpts); err != nil {
		return fmt.Errorf("certificate does not chain to ca bundle: %w", err)
	}

	// Check private key file permissions (best-effort, skip on Windows
	// where file modes are not meaningful).
	if err := checkKeyPermissions(cfg.KeyPath); err != nil {
		return err
	}

	return nil
}

// loadFirstCert reads a PEM file and returns the first certificate.
func loadFirstCert(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in %s", path)
	}
	return x509.ParseCertificate(block.Bytes)
}

// loadCertBundle reads a PEM file and returns all certificates in it.
func loadCertBundle(path string) ([]*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var certs []*x509.Certificate
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse certificate in %s: %w", path, err)
		}
		certs = append(certs, cert)
	}

	if len(certs) == 0 {
		return nil, fmt.Errorf("no certificates found in %s", path)
	}
	return certs, nil
}

// checkKeyPermissions verifies the private key file is not world-readable.
func checkKeyPermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	// Warn if group or others can read the key.
	if mode&0o077 != 0 {
		return fmt.Errorf("private key %s has overly permissive mode %04o (want 0600)", path, mode)
	}
	return nil
}

// Compile-time interface check.
var _ CertificateProvider = (*FilesystemProvider)(nil)
