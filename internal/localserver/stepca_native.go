package localserver

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
	stepapi "github.com/smallstep/certificates/api"
	ca "github.com/smallstep/certificates/ca"
)

// Package-level function variables for testability.
var (
	requestCertJWKFn  = requestCertJWK
	requestCertACMEFn = requestCertACME
)

// CertRequesterFunc is the signature for certificate request functions.
type CertRequesterFunc func(*StepCAConfig, []string, string, string, *slog.Logger) error

// SetCertRequestFuncs overrides the JWK and ACME cert request functions.
// It returns a restore function that should be called (e.g. via t.Cleanup)
// to reset the originals. This is exported for use by e2e tests in other
// packages.
func SetCertRequestFuncs(jwk, acme CertRequesterFunc) func() {
	oldJWK := requestCertJWKFn
	oldACME := requestCertACMEFn
	if jwk != nil {
		requestCertJWKFn = jwk
	}
	if acme != nil {
		requestCertACMEFn = acme
	}
	return func() {
		requestCertJWKFn = oldJWK
		requestCertACMEFn = oldACME
	}
}

// RequestCertJWK obtains a certificate from Step CA using the JWK provisioner.
// The provisioner password is read from stepCA.ProvisionerPasswordFile, or
// prompted from stdin if not set. Exported for use by CLI commands.
func RequestCertJWK(stepCA *StepCAConfig, sans []string, certPath, keyPath string, logger *slog.Logger) error {
	return requestCertJWK(stepCA, sans, certPath, keyPath, logger)
}

func requestCertJWK(stepCA *StepCAConfig, sans []string, certPath, keyPath string, logger *slog.Logger) error {
	// Read provisioner password.
	password, err := readProvisionerPassword(stepCA.ProvisionerPasswordFile)
	if err != nil {
		return fmt.Errorf("read provisioner password: %w", err)
	}

	provName := stepCA.Provisioner
	if provName == "" || strings.EqualFold(provName, "acme") {
		provName = "" // let the SDK pick the default JWK provisioner
	}

	logger.Info("requesting certificate via JWK provisioner", "url", stepCA.URL, "provisioner", provName)

	// Build a transport that trusts both system CAs and the Step CA root.
	tr, err := combinedTrustTransport(stepCA.RootPath)
	if err != nil {
		return fmt.Errorf("build TLS transport: %w", err)
	}

	// Discover the provisioner and decrypt its key with the password.
	prov, err := ca.NewProvisioner(provName, "", stepCA.URL, password, ca.WithTransport(tr))
	if err != nil {
		return fmt.Errorf("init JWK provisioner: %w", err)
	}

	// Mint a one-time token for the certificate request.
	token, err := prov.Token(sans[0], sans...)
	if err != nil {
		return fmt.Errorf("create provisioner token: %w", err)
	}

	// Generate a CSR and private key from the token.
	signReq, privKey, err := ca.CreateSignRequest(token)
	if err != nil {
		return fmt.Errorf("create sign request: %w", err)
	}

	// Let the CA's provisioner decide the validity period — don't override
	// NotAfter. The default JWK provisioner typically allows 24h max.

	logger.Debug("sign request",
		"cn", signReq.CsrPEM.Subject.CommonName,
		"dns_names", signReq.CsrPEM.DNSNames,
		"ips", signReq.CsrPEM.IPAddresses,
	)

	// Sign the certificate.
	client, err := ca.NewClient(stepCA.URL, ca.WithTransport(tr))
	if err != nil {
		return fmt.Errorf("create Step CA client: %w", err)
	}

	resp, err := client.Sign(signReq)
	if err != nil {
		return fmt.Errorf("sign certificate (CN=%s, SANs=%v): %w",
			signReq.CsrPEM.Subject.CommonName, signReq.CsrPEM.DNSNames, err)
	}

	// Write the certificate PEM.
	if err := writeCertPEM(certPath, resp); err != nil {
		return fmt.Errorf("write certificate: %w", err)
	}

	// Write the private key PEM.
	if err := writeKeyPEM(keyPath, privKey); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}

	logger.Info("obtained certificate via JWK provisioner", "cert", certPath, "sans", sans)
	return nil
}

// RequestCertACME obtains a certificate from Step CA using the ACME provisioner.
// It starts a temporary HTTP server on port 80 to solve the HTTP-01 challenge.
// Exported for use by CLI commands.
func RequestCertACME(stepCA *StepCAConfig, sans []string, certPath, keyPath string, logger *slog.Logger) error {
	return requestCertACME(stepCA, sans, certPath, keyPath, logger)
}

func requestCertACME(stepCA *StepCAConfig, sans []string, certPath, keyPath string, logger *slog.Logger) error {
	provName := stepCA.Provisioner
	if provName == "" {
		provName = "acme"
	}

	logger.Info("requesting certificate via ACME provisioner", "url", stepCA.URL, "provisioner", provName)

	// Generate an ephemeral ECDSA account key.
	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ACME account key: %w", err)
	}

	user := &acmeUser{key: accountKey}

	// Configure the lego client.
	config := lego.NewConfig(user)
	config.CADirURL = stepCA.URL + "/acme/" + provName + "/directory"
	config.Certificate.KeyType = certcrypto.EC384 // match existing P-384 convention

	// Build a cert pool that trusts both system CAs (for the Step CA server's
	// own TLS cert, which may be from Let's Encrypt or a reverse proxy) and
	// the Step CA root (for verifying issued certificates).
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	rootPEM, err := os.ReadFile(stepCA.RootPath)
	if err != nil {
		return fmt.Errorf("read Step CA root cert: %w", err)
	}
	pool.AppendCertsFromPEM(rootPEM)

	config.HTTPClient = &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    pool,
				MinVersion: tls.VersionTLS12,
			},
		},
		Timeout: 30 * time.Second,
	}

	client, err := lego.NewClient(config)
	if err != nil {
		return fmt.Errorf("create ACME client: %w", err)
	}

	// Use HTTP-01 challenge with a standalone server on port 80.
	httpProvider := http01.NewProviderServer("", "80")
	if err := client.Challenge.SetHTTP01Provider(httpProvider); err != nil {
		return fmt.Errorf("set HTTP-01 provider: %w", err)
	}

	// Register an ACME account.
	reg, err := client.Registration.Register(registration.RegisterOptions{
		TermsOfServiceAgreed: true,
	})
	if err != nil {
		return fmt.Errorf("register ACME account: %w", err)
	}
	user.registration = reg

	// Request the certificate.
	notAfter := time.Now().Add(90 * 24 * time.Hour)
	resource, err := client.Certificate.Obtain(certificate.ObtainRequest{
		Domains:  sans,
		Bundle:   true,
		NotAfter: notAfter,
	})
	if err != nil {
		return fmt.Errorf("obtain ACME certificate: %w", err)
	}

	// Write cert and key (lego returns PEM-encoded bytes).
	if err := os.WriteFile(certPath, resource.Certificate, 0o644); err != nil {
		return fmt.Errorf("write certificate: %w", err)
	}
	if err := os.WriteFile(keyPath, resource.PrivateKey, 0o600); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}

	logger.Info("obtained certificate via ACME provisioner", "cert", certPath, "sans", sans)
	return nil
}

// readProvisionerPassword reads the provisioner password from a file, or
// prompts from stdin if no file is specified.
func readProvisionerPassword(passwordFile string) ([]byte, error) {
	if passwordFile != "" {
		data, err := os.ReadFile(passwordFile)
		if err != nil {
			return nil, fmt.Errorf("read password file %q: %w", passwordFile, err)
		}
		return []byte(strings.TrimRight(string(data), "\n\r")), nil
	}

	// Prompt from stdin.
	fmt.Fprint(os.Stderr, "Provisioner password: ")
	var pw string
	if _, err := fmt.Scanln(&pw); err != nil {
		return nil, fmt.Errorf("read password from stdin: %w", err)
	}
	return []byte(pw), nil
}

// writeCertPEM writes the certificate chain from a Step CA SignResponse to a PEM file.
func writeCertPEM(path string, resp *stepapi.SignResponse) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	// Write the leaf certificate.
	if err := pem.Encode(f, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: resp.ServerPEM.Raw,
	}); err != nil {
		return err
	}

	// Write the intermediate (issuing CA) certificate if present.
	if resp.CaPEM.Certificate != nil {
		if err := pem.Encode(f, &pem.Block{
			Type:  "CERTIFICATE",
			Bytes: resp.CaPEM.Raw,
		}); err != nil {
			return err
		}
	}

	return nil
}

// writeKeyPEM marshals a private key to PKCS#8 PEM and writes it.
func writeKeyPEM(path string, key crypto.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal private key: %w", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	return os.WriteFile(path, pem.EncodeToMemory(block), 0o600)
}

// acmeUser implements the lego registration.User interface.
type acmeUser struct {
	key          crypto.PrivateKey
	registration *registration.Resource
}

func (u *acmeUser) GetEmail() string                        { return "" }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.registration }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// combinedTrustTransport builds an http.Transport that trusts both system CAs
// and the Step CA root certificate. This is necessary when the Step CA server's
// own TLS certificate is issued by a public CA (e.g. Let's Encrypt) while the
// certificates it issues are signed by its own root.
func combinedTrustTransport(rootCertPath string) (*http.Transport, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if rootCertPath != "" {
		rootPEM, err := os.ReadFile(rootCertPath)
		if err != nil {
			return nil, fmt.Errorf("read root cert %q: %w", rootCertPath, err)
		}
		pool.AppendCertsFromPEM(rootPEM)
	}
	return &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		},
	}, nil
}
