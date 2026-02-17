package pki

import "crypto/x509"

// NewCertPoolFromPEM creates a cert pool from PEM-encoded certificate data.
func NewCertPoolFromPEM(pemData []byte) *x509.CertPool {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		return nil
	}
	return pool
}

// VerifyOpts returns x509 verify options using the given root pool.
func VerifyOpts(roots *x509.CertPool) x509.VerifyOptions {
	return x509.VerifyOptions{Roots: roots}
}
