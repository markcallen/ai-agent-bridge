package localserver

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"

	"github.com/markcallen/ai-agent-bridge/internal/pki"
)

// safeNameRe matches a simple filename component: alphanumeric, hyphens, underscores, dots.
var safeNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// PKIMaterial holds resolved paths to all PKI files needed for secure mode.
type PKIMaterial struct {
	CACertPath      string // certs/ca.crt
	CAKeyPath       string // certs/ca.key
	ServerCertPath  string // certs/server.crt
	ServerKeyPath   string // certs/server.key
	LocalClientCert string // certs/local-client.crt
	LocalClientKey  string // certs/local-client.key
	CABundlePath    string // certs/ca-bundle.crt
	JWTSigningKey   string // certs/jwt-signing.key
	JWTSigningPub   string // certs/jwt-signing.pub
}

// CertsDir returns the path to the certs subdirectory within the state dir.
func CertsDir(stateDir string) string {
	return filepath.Join(stateDir, "certs")
}

// LoadPKIMaterial returns a PKIMaterial with resolved paths for an existing
// certs directory. It does not check whether the files exist.
func LoadPKIMaterial(stateDir string) *PKIMaterial {
	dir := CertsDir(stateDir)
	return &PKIMaterial{
		CACertPath:      filepath.Join(dir, "ca.crt"),
		CAKeyPath:       filepath.Join(dir, "ca.key"),
		ServerCertPath:  filepath.Join(dir, "server.crt"),
		ServerKeyPath:   filepath.Join(dir, "server.key"),
		LocalClientCert: filepath.Join(dir, "local-client.crt"),
		LocalClientKey:  filepath.Join(dir, "local-client.key"),
		CABundlePath:    filepath.Join(dir, "ca-bundle.crt"),
		JWTSigningKey:   filepath.Join(dir, "jwt-signing.key"),
		JWTSigningPub:   filepath.Join(dir, "jwt-signing.pub"),
	}
}

// EnsurePKI ensures that all PKI material exists in stateDir/certs/. If the
// CA certificate is missing, the entire set is generated from scratch:
//   - CA cert/key (ECDSA P-384, 10-year validity)
//   - Server cert/key with the provided SANs (90-day validity)
//   - Local-client cert/key for CLI's own connections
//   - CA trust bundle
//   - Ed25519 JWT signing keypair
//
// If the CA already exists, this is a no-op and returns existing paths.
func EnsurePKI(stateDir string, serverSANs []string, logger *slog.Logger) (*PKIMaterial, error) {
	mat := LoadPKIMaterial(stateDir)
	certsDir := CertsDir(stateDir)

	// Use ca.crt as sentinel — if it exists, assume the full set does.
	if _, err := os.Stat(mat.CACertPath); err == nil {
		logger.Info("PKI material already exists", "dir", certsDir)
		return mat, nil
	}

	logger.Info("generating PKI material", "dir", certsDir)

	if err := os.MkdirAll(certsDir, 0o700); err != nil {
		return nil, fmt.Errorf("create certs dir: %w", err)
	}

	// 1. Generate CA.
	caCertPath, caKeyPath, err := pki.InitCA("ai-agent-bridge", certsDir)
	if err != nil {
		return nil, fmt.Errorf("init CA: %w", err)
	}
	mat.CACertPath = caCertPath
	mat.CAKeyPath = caKeyPath
	logger.Info("generated CA", "cert", caCertPath)

	// Load CA for signing.
	caCert, caKey, err := pki.LoadCA(caCertPath, caKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load CA: %w", err)
	}

	// 2. Issue server certificate with SANs.
	serverCert, serverKey, err := pki.IssueCert(caCert, caKey, pki.CertTypeServer, "server", serverSANs, certsDir)
	if err != nil {
		return nil, fmt.Errorf("issue server cert: %w", err)
	}
	mat.ServerCertPath = serverCert
	mat.ServerKeyPath = serverKey
	logger.Info("generated server cert", "cert", serverCert, "sans", serverSANs)

	// 3. Issue local-client certificate for CLI connections.
	clientCert, clientKey, err := pki.IssueCert(caCert, caKey, pki.CertTypeClient, "local-client", nil, certsDir)
	if err != nil {
		return nil, fmt.Errorf("issue local-client cert: %w", err)
	}
	mat.LocalClientCert = clientCert
	mat.LocalClientKey = clientKey
	logger.Info("generated local-client cert", "cert", clientCert)

	// 4. Build CA trust bundle (just the CA cert for now).
	bundlePath := filepath.Join(certsDir, "ca-bundle.crt")
	if err := pki.BuildBundle(bundlePath, caCertPath); err != nil {
		return nil, fmt.Errorf("build CA bundle: %w", err)
	}
	mat.CABundlePath = bundlePath
	logger.Info("built CA bundle", "path", bundlePath)

	// 5. Generate Ed25519 JWT signing keypair.
	pubPath, privPath, err := pki.GenerateJWTKeypair(certsDir, "jwt-signing")
	if err != nil {
		return nil, fmt.Errorf("generate JWT keypair: %w", err)
	}
	mat.JWTSigningPub = pubPath
	mat.JWTSigningKey = privPath
	logger.Info("generated JWT signing keypair", "pub", pubPath)

	return mat, nil
}

// IssueClientCert issues a new client certificate signed by the existing CA.
// The cert is written to stateDir/certs/clients/<clientName>/.
// Returns paths to the cert and key files.
func IssueClientCert(stateDir, clientName string, logger *slog.Logger) (certPath, keyPath string, err error) {
	// Validate client name to prevent path traversal (e.g. "../shared").
	if !safeNameRe.MatchString(clientName) {
		return "", "", fmt.Errorf("invalid client name %q: must be alphanumeric with hyphens, underscores, or dots", clientName)
	}

	mat := LoadPKIMaterial(stateDir)

	// Load existing CA.
	caCert, caKey, err := pki.LoadCA(mat.CACertPath, mat.CAKeyPath)
	if err != nil {
		return "", "", fmt.Errorf("load CA (run 'server start --listen' first to generate PKI): %w", err)
	}

	outDir := filepath.Join(CertsDir(stateDir), "clients", clientName)
	certPath, keyPath, err = pki.IssueCert(caCert, caKey, pki.CertTypeClient, clientName, nil, outDir)
	if err != nil {
		return "", "", fmt.Errorf("issue client cert: %w", err)
	}

	// Generate a per-client JWT keypair so compromising one client doesn't
	// compromise JWT auth for all clients.
	jwtPubPath, jwtKeyPath, err := pki.GenerateJWTKeypair(outDir, "jwt-signing")
	if err != nil {
		return "", "", fmt.Errorf("generate client JWT keypair: %w", err)
	}

	// Register the client's public key with the server so the verifier
	// accepts tokens signed by this client.
	serverJWTDir := filepath.Join(CertsDir(stateDir), "jwt-clients")
	if err := os.MkdirAll(serverJWTDir, 0o700); err != nil {
		return "", "", fmt.Errorf("create jwt-clients dir: %w", err)
	}
	pubData, err := os.ReadFile(jwtPubPath)
	if err != nil {
		return "", "", fmt.Errorf("read client JWT public key: %w", err)
	}
	if err := os.WriteFile(filepath.Join(serverJWTDir, clientName+".pub"), pubData, 0o644); err != nil {
		return "", "", fmt.Errorf("write client JWT public key: %w", err)
	}

	logger.Info("issued client credentials", "name", clientName, "cert", certPath, "jwt_key", jwtKeyPath)
	return certPath, keyPath, nil
}
