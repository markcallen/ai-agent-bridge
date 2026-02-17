package bridgeclient

import "time"

// MTLSConfig holds paths for mTLS client credentials.
type MTLSConfig struct {
	CABundlePath string // Trust bundle (own CA + cross-signed CAs)
	CertPath     string // Client certificate
	KeyPath      string // Client private key
	ServerName   string // Expected server name for verification
}

// JWTConfig holds configuration for automatic JWT minting.
type JWTConfig struct {
	PrivateKeyPath string // Ed25519 private key for signing
	Issuer         string // JWT issuer claim
	Audience       string // JWT audience claim
	TTL            time.Duration
}

// Option configures a Client.
type Option func(*clientConfig)

type clientConfig struct {
	target  string
	mtls    *MTLSConfig
	jwt     *JWTConfig
	timeout time.Duration
}

// WithTarget sets the bridge daemon address (host:port).
func WithTarget(addr string) Option {
	return func(c *clientConfig) { c.target = addr }
}

// WithMTLS configures mTLS credentials for the connection.
func WithMTLS(cfg MTLSConfig) Option {
	return func(c *clientConfig) { c.mtls = &cfg }
}

// WithJWT configures automatic JWT minting for each RPC call.
func WithJWT(cfg JWTConfig) Option {
	return func(c *clientConfig) { c.jwt = &cfg }
}

// WithTimeout sets the default per-call timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *clientConfig) { c.timeout = d }
}
