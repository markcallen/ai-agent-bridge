package localserver

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/markcallen/ai-agent-bridge/internal/pki"
)

// safeNameRe matches a simple filename component: alphanumeric, hyphens, underscores, dots.
var safeNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// StepCAConfig holds optional Step CA integration settings. When URL is set,
// EnsurePKI delegates certificate issuance to the Step CA instance instead of
// generating a self-signed CA. The `step` CLI must be on PATH.
type StepCAConfig struct {
	// URL is the Step CA server URL (e.g. "https://step-ca.internal:443").
	URL string
	// RootPath is the path to the Step CA root certificate used to verify
	// the CA server's TLS connection.
	RootPath string
	// OIDCProviderURL is the OIDC issuer URL configured as a Step CA
	// provisioner (e.g. "https://accounts.google.com"). Used by
	// IssueClientCertViaOIDC to select the correct provisioner.
	OIDCProviderURL string
}

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

// EnsurePKI ensures that all PKI material exists in stateDir/certs/.
//
// When stepCA is nil (default), the entire set is auto-generated:
//   - CA cert/key (ECDSA P-384, 10-year validity)
//   - Server cert/key with the provided SANs (90-day validity)
//   - Local-client cert/key for CLI's own connections
//   - CA trust bundle
//   - Ed25519 JWT signing keypair
//
// When stepCA is non-nil, auto-generation is skipped. Instead, the Step CA
// root is copied to ca-bundle.crt and the server certificate is obtained from
// Step CA via the `step` CLI. The JWT keypair is still generated locally.
//
// If the CA bundle already exists, this is a no-op and returns existing paths.
func EnsurePKI(stateDir string, serverSANs []string, logger *slog.Logger, stepCA *StepCAConfig) (*PKIMaterial, error) {
	mat := LoadPKIMaterial(stateDir)
	certsDir := CertsDir(stateDir)

	// Use ca-bundle.crt as sentinel — it is produced by both auto-PKI and
	// Step CA paths, so checking it covers both cases.
	if _, err := os.Stat(mat.CABundlePath); err == nil {
		logger.Info("PKI material already exists", "dir", certsDir)
		return mat, nil
	}

	if err := os.MkdirAll(certsDir, 0o700); err != nil {
		return nil, fmt.Errorf("create certs dir: %w", err)
	}

	if stepCA != nil && stepCA.URL != "" {
		return ensurePKIStepCA(stateDir, serverSANs, logger, stepCA, mat, certsDir)
	}
	return ensurePKIAutoGen(stateDir, serverSANs, logger, mat, certsDir)
}

// ensurePKIAutoGen generates a self-signed CA and all derived material.
func ensurePKIAutoGen(stateDir string, serverSANs []string, logger *slog.Logger, mat *PKIMaterial, certsDir string) (*PKIMaterial, error) {
	logger.Info("generating PKI material", "dir", certsDir)

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

// ensurePKIStepCA uses the Step CA CLI to obtain a server certificate.
// The Step CA root cert is copied to ca-bundle.crt; JWT material is generated
// locally as in auto-PKI mode (tokens are still validated per-client).
func ensurePKIStepCA(stateDir string, serverSANs []string, logger *slog.Logger, stepCA *StepCAConfig, mat *PKIMaterial, certsDir string) (*PKIMaterial, error) {
	logger.Info("using Step CA for PKI", "url", stepCA.URL)

	// 1. Copy Step CA root cert to ca-bundle.crt so clients can verify the server.
	if stepCA.RootPath == "" {
		return nil, fmt.Errorf("step-ca-root is required when --step-ca-url is set")
	}
	bundlePath := filepath.Join(certsDir, "ca-bundle.crt")
	if err := copyFile(stepCA.RootPath, bundlePath); err != nil {
		return nil, fmt.Errorf("copy Step CA root to ca-bundle: %w", err)
	}
	mat.CABundlePath = bundlePath
	// In Step CA mode there is no local CA key — leave mat.CACertPath and
	// mat.CAKeyPath empty; they are only used by IssueClientCert (auto-PKI path).
	mat.CACertPath = bundlePath
	logger.Info("copied Step CA root", "bundle", bundlePath)

	// 2. Obtain server certificate from Step CA via `step ca certificate`.
	sans := serverSANs
	if len(sans) == 0 {
		sans = []string{"server"}
	}
	serverCert := filepath.Join(certsDir, "server.crt")
	serverKey := filepath.Join(certsDir, "server.key")

	stepArgs := []string{
		"ca", "certificate",
		sans[0],
		serverCert,
		serverKey,
		"--ca-url", stepCA.URL,
		"--root", stepCA.RootPath,
		"--not-after", "2160h", // 90 days
		"--force",
	}
	for _, san := range sans[1:] {
		stepArgs = append(stepArgs, "--san", san)
	}
	if err := runStep(stepArgs, logger); err != nil {
		return nil, fmt.Errorf("obtain server cert from Step CA: %w", err)
	}
	mat.ServerCertPath = serverCert
	mat.ServerKeyPath = serverKey
	logger.Info("obtained server cert from Step CA", "cert", serverCert)

	// 3. Generate a local CA for CLI/local-client credentials.
	// The server cert comes from Step CA; this small CA signs only the local
	// management cert so operators can use bridgectl locally against a secure server.
	localCACert, localCAKey, err := pki.InitCA("ai-agent-bridge-local", certsDir)
	if err != nil {
		return nil, fmt.Errorf("init local CA for Step CA mode: %w", err)
	}
	mat.CACertPath = localCACert
	mat.CAKeyPath = localCAKey
	logger.Info("generated local CA for CLI credentials", "cert", localCACert)

	localCA, localKey, err := pki.LoadCA(localCACert, localCAKey)
	if err != nil {
		return nil, fmt.Errorf("load local CA: %w", err)
	}

	clientCert, clientKey, err := pki.IssueCert(localCA, localKey, pki.CertTypeClient, "local-client", nil, certsDir)
	if err != nil {
		return nil, fmt.Errorf("issue local-client cert: %w", err)
	}
	mat.LocalClientCert = clientCert
	mat.LocalClientKey = clientKey
	logger.Info("generated local-client cert", "cert", clientCert)

	// Append the local CA to the trust bundle so the server accepts local-client.
	if err := pki.AppendBundle(bundlePath, localCACert); err != nil {
		return nil, fmt.Errorf("append local CA to bundle: %w", err)
	}
	logger.Info("appended local CA to trust bundle", "bundle", bundlePath)

	// 4. Generate JWT keypair locally (same as auto-PKI path).
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

// IssueClientCertViaOIDC obtains a short-lived client certificate from Step CA
// using the OIDC provisioner. It opens a browser for the OIDC login flow and
// waits for Step CA to issue the certificate. The `step` CLI must be on PATH.
//
// The cert and key are written to stateDir/certs/clients/<clientName>/ (same
// layout as IssueClientCert) so the same SDK configuration works for both
// auto-PKI and Step CA clients.
func IssueClientCertViaOIDC(stateDir, clientName string, stepCA *StepCAConfig, logger *slog.Logger) (certPath, keyPath string, err error) {
	if !safeNameRe.MatchString(clientName) {
		return "", "", fmt.Errorf("invalid client name %q: must be alphanumeric with hyphens, underscores, or dots", clientName)
	}
	if stepCA == nil || stepCA.URL == "" {
		return "", "", fmt.Errorf("--step-ca-url is required for OIDC enrollment")
	}
	if stepCA.OIDCProviderURL == "" {
		return "", "", fmt.Errorf("--oidc-provider is required for OIDC enrollment")
	}
	if stepCA.RootPath == "" {
		return "", "", fmt.Errorf("--step-ca-root is required for OIDC enrollment")
	}

	outDir := filepath.Join(CertsDir(stateDir), "clients", clientName)
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return "", "", fmt.Errorf("create client dir: %w", err)
	}

	certPath = filepath.Join(outDir, clientName+".crt")
	keyPath = filepath.Join(outDir, clientName+".key")

	stepArgs := []string{
		"ca", "certificate",
		clientName,
		certPath,
		keyPath,
		"--provisioner", stepCA.OIDCProviderURL,
		"--ca-url", stepCA.URL,
		"--root", stepCA.RootPath,
		"--not-after", "24h",
		"--force",
	}
	logger.Info("starting OIDC enrollment via Step CA", "name", clientName, "oidc_provider", stepCA.OIDCProviderURL)
	if err := runStep(stepArgs, logger); err != nil {
		return "", "", fmt.Errorf("obtain client cert via OIDC: %w", err)
	}

	// Generate a per-client JWT keypair (same as auto-PKI path).
	jwtPubPath, jwtKeyPath, err := pki.GenerateJWTKeypair(outDir, "jwt-signing")
	if err != nil {
		return "", "", fmt.Errorf("generate client JWT keypair: %w", err)
	}

	// Register the client's public key server-side.
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

	logger.Info("issued OIDC client credentials", "name", clientName, "cert", certPath, "jwt_key", jwtKeyPath)
	return certPath, keyPath, nil
}

// runStep executes the `step` CLI with the given arguments, inheriting stderr
// and stdout (Step CA prints login URLs and status to stdout).
func runStep(args []string, logger *slog.Logger) error {
	stepBin, err := exec.LookPath("step")
	if err != nil {
		return fmt.Errorf("'step' CLI not found on PATH — install it from https://smallstep.com/cli/: %w", err)
	}
	logger.Debug("running step CLI", "args", args)
	cmd := exec.Command(stepBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// copyFile copies src to dst, creating dst with mode 0o644.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	if closeErr := out.Close(); closeErr != nil && copyErr == nil {
		return closeErr
	}
	return copyErr
}
