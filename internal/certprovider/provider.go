// Package certprovider defines the CertificateProvider interface and related
// types used to abstract certificate issuance, renewal, and trust-root
// discovery away from any specific PKI implementation.
package certprovider

import (
	"context"
	"crypto/x509"
	"errors"
	"time"
)

// ErrRenewalNotSupported is returned by providers that do not support
// automated certificate renewal (e.g. the filesystem provider).
var ErrRenewalNotSupported = errors.New("certificate renewal not supported by this provider")

// CertificateProvider abstracts the source of X.509 certificate material.
// Implementations include a filesystem provider (static certs on disk),
// an auto provider (self-signed CA), and a Step CA provider (automated PKI).
type CertificateProvider interface {
	// Enroll obtains a new certificate from the provider. For providers
	// that manage their own CA (e.g. stepca), this performs a full CSR
	// flow. For passive providers (e.g. filesystem), this returns the
	// pre-configured identity without issuing anything new.
	Enroll(ctx context.Context, req EnrollmentRequest) (*Identity, error)

	// Renew replaces the certificate in identity with a freshly issued
	// one. Providers that do not support renewal return
	// ErrRenewalNotSupported.
	Renew(ctx context.Context, identity *Identity) error

	// Roots returns the trusted CA certificates used to verify
	// certificates issued by this provider.
	Roots(ctx context.Context) ([]*x509.Certificate, error)
}

// CertRole indicates whether a certificate is intended for a server or client.
type CertRole string

const (
	// RoleServer identifies a server (TLS listener) certificate.
	RoleServer CertRole = "server"
	// RoleClient identifies a client (TLS dialer) certificate.
	RoleClient CertRole = "client"
)

// EnrollmentRequest contains the parameters needed to request a certificate.
type EnrollmentRequest struct {
	// CommonName is the subject CN for the issued certificate.
	CommonName string
	// SANs are additional DNS names or IP addresses for the certificate.
	SANs []string
	// Token is an optional one-time enrollment credential.
	Token string
	// Validity is the requested certificate lifetime. Providers may
	// impose their own maximum.
	Validity time.Duration
	// Role selects whether to issue a server or client certificate.
	Role CertRole
	// OutDir is the directory where certificate and key files should be
	// written. Providers that write to disk use this path.
	OutDir string
}

// Identity represents an issued certificate and its associated material.
type Identity struct {
	// CertPath is the path to the PEM-encoded certificate (or chain).
	CertPath string
	// KeyPath is the path to the PEM-encoded private key.
	KeyPath string
	// CACertPath is the path to the CA bundle used to verify this
	// identity.
	CACertPath string
	// Provider is the name of the provider that issued this identity
	// (e.g. "filesystem", "auto", "stepca").
	Provider string
	// CommonName is the subject CN of the certificate.
	CommonName string
	// NotBefore is the start of the certificate validity window.
	NotBefore time.Time
	// NotAfter is the end of the certificate validity window.
	NotAfter time.Time
	// Renewable indicates whether the provider supports automatic
	// renewal for this identity.
	Renewable bool
}

// NeedsRenewal reports whether the identity's certificate should be renewed.
// It returns true when less than one-third of the original validity period
// remains, matching the standard renewal heuristic.
func (id *Identity) NeedsRenewal() bool {
	total := id.NotAfter.Sub(id.NotBefore)
	remaining := time.Until(id.NotAfter)
	return remaining < total/3
}
