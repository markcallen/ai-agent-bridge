package auth

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
	"time"
)

// TLSConfig holds paths for mTLS configuration.
type TLSConfig struct {
	CABundlePath string // Trust bundle (own CA + cross-signed CAs)
	CertPath     string // Server or client certificate
	KeyPath      string // Server or client private key
	ServerName   string // For client-side server name verification
}

// CertReloader watches TLS certificate and key files on disk and reloads
// them when the files change. It is safe for concurrent use. The reload
// check is triggered on each TLS handshake via the GetCertificate callback,
// so renewed certificates are picked up without restarting the server.
type CertReloader struct {
	certPath string
	keyPath  string

	mu      sync.RWMutex
	cert    *tls.Certificate
	modTime time.Time
}

// NewCertReloader creates a CertReloader that loads the certificate from
// the given paths. The initial certificate is loaded immediately.
func NewCertReloader(certPath, keyPath string) (*CertReloader, error) {
	r := &CertReloader{
		certPath: certPath,
		keyPath:  keyPath,
	}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// GetCertificate implements the tls.Config.GetCertificate callback.
// On each call it checks whether the cert file has been modified on disk
// and reloads if necessary. If the reload fails, the previously cached
// certificate is returned.
func (r *CertReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.maybeReload()
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cert, nil
}

// Reload forces an immediate reload of the certificate from disk.
// Returns an error if the cert files cannot be read or parsed; the
// previously cached certificate is preserved in that case.
func (r *CertReloader) Reload() error {
	return r.reload()
}

func (r *CertReloader) maybeReload() {
	info, err := os.Stat(r.certPath)
	if err != nil {
		return
	}
	r.mu.RLock()
	same := info.ModTime().Equal(r.modTime)
	r.mu.RUnlock()
	if same {
		return
	}
	_ = r.reload()
}

func (r *CertReloader) reload() error {
	cert, err := tls.LoadX509KeyPair(r.certPath, r.keyPath)
	if err != nil {
		return fmt.Errorf("reload cert %s: %w", r.certPath, err)
	}
	info, _ := os.Stat(r.certPath)
	var modTime time.Time
	if info != nil {
		modTime = info.ModTime()
	}
	r.mu.Lock()
	r.cert = &cert
	r.modTime = modTime
	r.mu.Unlock()
	return nil
}

// ServerTLSConfig returns a TLS config that REQUIRES and verifies client certs (mTLS).
// Minimum TLS 1.3. The server certificate is loaded via a CertReloader so that
// renewed certificates on disk are picked up on new TLS handshakes without
// restarting the server.
func ServerTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	caPool, err := loadCAPool(cfg.CABundlePath)
	if err != nil {
		return nil, err
	}

	reloader, err := NewCertReloader(cfg.CertPath, cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("init cert reloader: %w", err)
	}

	return &tls.Config{
		MinVersion:     tls.VersionTLS13,
		GetCertificate: reloader.GetCertificate,
		ClientAuth:     tls.RequireAndVerifyClientCert,
		ClientCAs:      caPool,
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
