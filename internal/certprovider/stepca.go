package certprovider

import (
	"context"
	"crypto/x509"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
)

// StepCAConfig holds the settings needed to communicate with a Step CA instance.
type StepCAConfig struct {
	// URL is the Step CA server URL (e.g. "https://step-ca.internal:443").
	URL string
	// RootPath is the path to the Step CA root certificate.
	RootPath string
	// Provisioner is the provisioner name (e.g. "acme", "bridge-jwk").
	Provisioner string
	// ProvisionerPasswordFile is the path to the JWK provisioner password file.
	ProvisionerPasswordFile string
}

// CertRequestFunc is the signature for functions that request a certificate
// from Step CA. It matches the pattern used by the localserver package's
// requestCertJWK and requestCertACME functions.
type CertRequestFunc func(cfg *StepCAConfig, sans []string, certPath, keyPath string, logger *slog.Logger) error

// CertRenewFunc is the signature for mTLS-based certificate renewal.
type CertRenewFunc func(cfg *StepCAConfig, certPath, keyPath string, logger *slog.Logger) error

// StepCAProvider implements CertificateProvider by delegating certificate
// operations to a Step CA instance. It uses injectable function variables
// so that tests can substitute mock implementations.
type StepCAProvider struct {
	cfg    StepCAConfig
	logger *slog.Logger

	// Injectable functions for testability. When nil, operations that
	// require them return errors.
	RequestJWK  CertRequestFunc
	RequestACME CertRequestFunc
	RenewMTLS   CertRenewFunc
}

// NewStepCAProvider creates a StepCAProvider with the given configuration.
// The request and renew functions must be injected by the caller — typically
// from the localserver package's exported function variables.
func NewStepCAProvider(cfg StepCAConfig, logger *slog.Logger) (*StepCAProvider, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("stepca provider: URL is required")
	}
	if cfg.RootPath == "" {
		return nil, fmt.Errorf("stepca provider: root certificate path is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &StepCAProvider{cfg: cfg, logger: logger}, nil
}

// Enroll obtains a new certificate from Step CA using the configured
// provisioner (JWK or ACME).
func (p *StepCAProvider) Enroll(_ context.Context, req EnrollmentRequest) (*Identity, error) {
	sans := req.SANs
	if len(sans) == 0 && req.CommonName != "" {
		sans = []string{req.CommonName}
	}

	outDir := req.OutDir
	if outDir == "" {
		return nil, fmt.Errorf("stepca enroll: OutDir is required")
	}

	cn := req.CommonName
	if cn == "" && len(sans) > 0 {
		cn = sans[0]
	}

	// Sanitize the CN to prevent path traversal: take only the base name
	// and replace spaces with hyphens.
	baseName := filepath.Base(strings.ReplaceAll(cn, " ", "-"))
	certPath := filepath.Join(outDir, baseName+".crt")
	keyPath := filepath.Join(outDir, baseName+".key")

	switch strings.ToLower(p.cfg.Provisioner) {
	case "acme":
		if p.RequestACME == nil {
			return nil, fmt.Errorf("stepca enroll: ACME request function not configured")
		}
		if err := p.RequestACME(&p.cfg, sans, certPath, keyPath, p.logger); err != nil {
			return nil, fmt.Errorf("stepca enroll (ACME): %w", err)
		}
	default:
		if p.RequestJWK == nil {
			return nil, fmt.Errorf("stepca enroll: JWK request function not configured")
		}
		if err := p.RequestJWK(&p.cfg, sans, certPath, keyPath, p.logger); err != nil {
			return nil, fmt.Errorf("stepca enroll (JWK): %w", err)
		}
	}

	// Load the issued cert to populate identity metadata.
	cert, err := loadFirstCert(certPath)
	if err != nil {
		return nil, fmt.Errorf("stepca enroll: load issued cert: %w", err)
	}

	return &Identity{
		CertPath:   certPath,
		KeyPath:    keyPath,
		CACertPath: p.cfg.RootPath,
		Provider:   "stepca",
		CommonName: cert.Subject.CommonName,
		NotBefore:  cert.NotBefore,
		NotAfter:   cert.NotAfter,
		Renewable:  true,
	}, nil
}

// Renew attempts to renew the certificate using mTLS-based renewal first,
// then falls back to a full provisioner-based request if mTLS renewal fails.
func (p *StepCAProvider) Renew(_ context.Context, identity *Identity) error {
	if p.RenewMTLS != nil {
		err := p.RenewMTLS(&p.cfg, identity.CertPath, identity.KeyPath, p.logger)
		if err == nil {
			// Reload the renewed cert to update identity metadata.
			return p.updateIdentityFromCert(identity)
		}
		// mTLS renewal failed — fall back to provisioner-based request.
		p.logger.Warn("mTLS renewal failed, falling back to provisioner-based request", "error", err)
	}

	// Fallback: re-enroll using the provisioner.
	sans := extractSANs(identity.CertPath)

	switch strings.ToLower(p.cfg.Provisioner) {
	case "acme":
		if p.RequestACME == nil {
			return fmt.Errorf("stepca renew: ACME request function not configured")
		}
		if err := p.RequestACME(&p.cfg, sans, identity.CertPath, identity.KeyPath, p.logger); err != nil {
			return fmt.Errorf("stepca renew (ACME fallback): %w", err)
		}
	default:
		if p.RequestJWK == nil {
			return fmt.Errorf("stepca renew: JWK request function not configured")
		}
		if p.cfg.ProvisionerPasswordFile == "" {
			return fmt.Errorf("stepca renew: provisioner_password_file required for non-interactive JWK renewal")
		}
		if err := p.RequestJWK(&p.cfg, sans, identity.CertPath, identity.KeyPath, p.logger); err != nil {
			return fmt.Errorf("stepca renew (JWK fallback): %w", err)
		}
	}

	return p.updateIdentityFromCert(identity)
}

// Roots returns the Step CA root certificate(s).
func (p *StepCAProvider) Roots(_ context.Context) ([]*x509.Certificate, error) {
	return loadCertBundle(p.cfg.RootPath)
}

// updateIdentityFromCert reloads the cert at identity.CertPath and updates
// the identity's time fields.
func (p *StepCAProvider) updateIdentityFromCert(identity *Identity) error {
	cert, err := loadFirstCert(identity.CertPath)
	if err != nil {
		return fmt.Errorf("stepca: reload renewed cert: %w", err)
	}
	identity.NotBefore = cert.NotBefore
	identity.NotAfter = cert.NotAfter
	return nil
}

// extractSANs reads a certificate and returns its DNS names and IP addresses
// as a SAN list suitable for re-enrollment.
func extractSANs(certPath string) []string {
	cert, err := loadFirstCert(certPath)
	if err != nil {
		return nil
	}
	sans := append([]string{}, cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		sans = append(sans, ip.String())
	}
	return sans
}

// Compile-time interface check.
var _ CertificateProvider = (*StepCAProvider)(nil)
