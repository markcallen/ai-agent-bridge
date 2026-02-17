package auth

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// TLSConfig holds paths for mTLS configuration.
type TLSConfig struct {
	CABundlePath string // Trust bundle (own CA + cross-signed CAs)
	CertPath     string // Server or client certificate
	KeyPath      string // Server or client private key
	ServerName   string // For client-side server name verification
}

// ServerTLSConfig returns a TLS config that REQUIRES and verifies client certs (mTLS).
// Minimum TLS 1.3.
func ServerTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	caPool, err := loadCAPool(cfg.CABundlePath)
	if err != nil {
		return nil, err
	}

	cert, err := tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("load server keypair: %w", err)
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
	}, nil
}

// ClientTLSConfig returns a TLS config that verifies server certs and presents a client cert (mTLS).
// Minimum TLS 1.3.
func ClientTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	caPool, err := loadCAPool(cfg.CABundlePath)
	if err != nil {
		return nil, err
	}

	cert, err := tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("load client keypair: %w", err)
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		ServerName:   cfg.ServerName,
	}, nil
}

func loadCAPool(path string) (*x509.CertPool, error) {
	caPEM, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ca bundle: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("no valid certs in ca bundle %s", path)
	}
	return pool, nil
}
