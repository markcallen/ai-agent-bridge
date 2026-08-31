package server

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	bridgev1 "github.com/orchael/bridgectl/gen/bridge/v1"
	"github.com/orchael/bridgectl/internal/pki"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// EnrollClient implements the EnrollClient RPC. It validates the one-time
// enrollment token, issues a certificate from the CSR, registers the client's
// JWT public key, and returns the cert + CA bundle.
//
// This RPC is exempt from mTLS and JWT auth in the interceptor chain — the
// enrollment token is the sole authorization credential.
func (s *BridgeServer) EnrollClient(ctx context.Context, req *bridgev1.EnrollClientRequest) (*bridgev1.EnrollClientResponse, error) {
	if !s.globalRL.allow("global") {
		return nil, status.Error(codes.ResourceExhausted, "global RPC rate limit exceeded")
	}

	if s.enrollStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "enrollment not configured on this server")
	}

	if req.Token == "" {
		return nil, status.Error(codes.InvalidArgument, "enrollment token is required")
	}
	if len(req.Csr) == 0 {
		return nil, status.Error(codes.InvalidArgument, "CSR is required")
	}
	if len(req.JwtPublicKey) == 0 {
		return nil, status.Error(codes.InvalidArgument, "JWT public key is required")
	}

	// Atomically validate and consume the enrollment token. This prevents
	// concurrent enrollment with the same token (the token is marked used
	// before we issue any certificate).
	tok, err := s.enrollStore.ValidateAndConsume(req.Token)
	if err != nil {
		s.logger.Warn("enrollment.failed", "error", err)
		// Return a generic error to avoid leaking token state to callers.
		return nil, status.Error(codes.PermissionDenied, "invalid or expired enrollment token")
	}

	// Parse the CSR.
	csr, err := x509.ParseCertificateRequest(req.Csr)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid CSR: %v", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "CSR signature verification failed: %v", err)
	}

	// Verify the CSR CN matches the token's intended identity.
	if csr.Subject.CommonName != tok.Identity {
		return nil, status.Errorf(codes.InvalidArgument,
			"CSR CN %q does not match enrollment identity %q",
			csr.Subject.CommonName, tok.Identity)
	}

	// Parse the JWT public key.
	pubKeyRaw, err := x509.ParsePKIXPublicKey(req.JwtPublicKey)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid JWT public key: %v", err)
	}
	edKey, ok := pubKeyRaw.(ed25519.PublicKey)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "JWT public key must be Ed25519")
	}

	// Issue the certificate. For now, use the auto provider to sign the
	// CSR with the local CA. In the future, this will delegate to the
	// configured CertificateProvider.
	certPEM, err := s.issueFromCSR(csr)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "issue certificate: %v", err)
	}

	// Load the CA bundle to return to the client.
	caBundlePEM, err := s.loadCABundle()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load CA bundle: %v", err)
	}

	// Register the JWT public key.
	if s.jwtVerifier != nil {
		issuer := tok.Identity
		if s.certsDir != "" {
			jwtClientsDir := filepath.Join(s.certsDir, "jwt-clients")
			if err := os.MkdirAll(jwtClientsDir, 0o700); err != nil {
				return nil, status.Errorf(codes.Internal, "create jwt-clients dir: %v", err)
			}
			pubPath := filepath.Join(jwtClientsDir, issuer+".pub")
			if err := pki.WritePublicKeyPEM(pubPath, edKey); err != nil {
				return nil, status.Errorf(codes.Internal, "write JWT public key: %v", err)
			}
		}
		s.jwtVerifier.AddKey(issuer, edKey)
	}

	// Parse the issued cert to get expiry.
	cert, err := parsePEMCert(certPEM)
	if err != nil {
		s.logger.Warn("enrollment.parse_cert_failed", "error", err)
	}

	s.logger.Info("enrollment.succeeded",
		"identity", tok.Identity,
		"issuer", tok.Identity,
	)

	resp := &bridgev1.EnrollClientResponse{
		Certificate: certPEM,
		CaBundle:    caBundlePEM,
		Issuer:      tok.Identity,
		Identity:    tok.Identity,
	}
	if cert != nil {
		resp.Expires = timestamppb.New(cert.NotAfter)
	}

	return resp, nil
}

// issueFromCSR signs a CSR using the server's local CA.
func (s *BridgeServer) issueFromCSR(csr *x509.CertificateRequest) ([]byte, error) {
	if s.certsDir == "" {
		return nil, fmt.Errorf("certs directory not configured")
	}

	caCertPath := filepath.Join(s.certsDir, "ca.crt")
	caKeyPath := filepath.Join(s.certsDir, "ca.key")

	caCert, caKey, err := pki.LoadCA(caCertPath, caKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load CA: %w", err)
	}

	// Issue a client cert with the CSR's CN and SANs.
	sans := append([]string{}, csr.DNSNames...)
	for _, ip := range csr.IPAddresses {
		sans = append(sans, ip.String())
	}

	outDir := filepath.Join(s.certsDir, "enrolled")
	certPath, _, err := pki.IssueCert(caCert, caKey, pki.CertTypeClient, csr.Subject.CommonName, sans, outDir, 0)
	if err != nil {
		return nil, fmt.Errorf("issue cert: %w", err)
	}

	return os.ReadFile(certPath)
}

// loadCABundle reads the CA bundle file.
func (s *BridgeServer) loadCABundle() ([]byte, error) {
	bundlePath := filepath.Join(s.certsDir, "ca-bundle.crt")
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		// Fall back to just the CA cert.
		caPath := filepath.Join(s.certsDir, "ca.crt")
		return os.ReadFile(caPath)
	}
	return data, nil
}

// parsePEMCert parses the first certificate from PEM data.
func parsePEMCert(pemData []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	return x509.ParseCertificate(block.Bytes)
}
