package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/markcallen/ai-agent-bridge/internal/pki"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	os.Args = append(os.Args[:1], os.Args[2:]...)

	switch cmd {
	case "init":
		cmdInit()
	case "issue":
		cmdIssue()
	case "cross-sign":
		cmdCrossSign()
	case "bundle":
		cmdBundle()
	case "jwt-keygen":
		cmdJWTKeygen()
	case "verify":
		cmdVerify()
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `bridge-ca - Certificate Authority management for ai-agent-bridge

Commands:
  init         Initialize a new CA
  issue        Issue a server or client certificate
  cross-sign   Cross-sign an external CA certificate
  bundle       Build a trust bundle from multiple CA certs
  jwt-keygen   Generate Ed25519 keypair for JWT signing
  verify       Verify a certificate against a trust bundle

Run 'bridge-ca <command> --help' for details.
`)
}

func cmdInit() {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	name := fs.String("name", "", "CA common name (required)")
	out := fs.String("out", "certs/", "Output directory")
	fs.Parse(os.Args[1:])

	if *name == "" {
		fmt.Fprintln(os.Stderr, "error: --name is required")
		os.Exit(1)
	}

	certPath, keyPath, err := pki.InitCA(*name, *out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("CA certificate: %s\n", certPath)
	fmt.Printf("CA private key: %s\n", keyPath)
}

func cmdIssue() {
	fs := flag.NewFlagSet("issue", flag.ExitOnError)
	certType := fs.String("type", "", "Certificate type: server or client (required)")
	cn := fs.String("cn", "", "Common name (required)")
	san := fs.String("san", "", "Subject alternative names (comma-separated)")
	caCert := fs.String("ca", "", "CA certificate path (required)")
	caKey := fs.String("ca-key", "", "CA private key path (required)")
	out := fs.String("out", "certs/", "Output directory")
	fs.Parse(os.Args[1:])

	if *certType == "" || *cn == "" || *caCert == "" || *caKey == "" {
		fmt.Fprintln(os.Stderr, "error: --type, --cn, --ca, and --ca-key are required")
		os.Exit(1)
	}

	var ct pki.CertType
	switch strings.ToLower(*certType) {
	case "server":
		ct = pki.CertTypeServer
	case "client":
		ct = pki.CertTypeClient
	default:
		fmt.Fprintf(os.Stderr, "error: --type must be 'server' or 'client', got %q\n", *certType)
		os.Exit(1)
	}

	ca, key, err := pki.LoadCA(*caCert, *caKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var sans []string
	if *san != "" {
		sans = strings.Split(*san, ",")
	}

	certPath, keyPath, err := pki.IssueCert(ca, key, ct, *cn, sans, *out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Certificate: %s\n", certPath)
	fmt.Printf("Private key: %s\n", keyPath)
}

func cmdCrossSign() {
	fs := flag.NewFlagSet("cross-sign", flag.ExitOnError)
	signerCA := fs.String("signer-ca", "", "Signer CA certificate path (required)")
	signerKey := fs.String("signer-key", "", "Signer CA private key path (required)")
	targetCA := fs.String("target-ca", "", "Target CA certificate to cross-sign (required)")
	out := fs.String("out", "", "Output path for cross-signed cert (required)")
	fs.Parse(os.Args[1:])

	if *signerCA == "" || *signerKey == "" || *targetCA == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "error: --signer-ca, --signer-key, --target-ca, and --out are required")
		os.Exit(1)
	}

	sCA, sKey, err := pki.LoadCA(*signerCA, *signerKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading signer CA: %v\n", err)
		os.Exit(1)
	}

	tCA, err := pki.LoadCert(*targetCA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading target CA: %v\n", err)
		os.Exit(1)
	}

	if err := pki.CrossSign(sCA, sKey, tCA, *out); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Cross-signed certificate: %s\n", *out)
}

func cmdBundle() {
	fs := flag.NewFlagSet("bundle", flag.ExitOnError)
	out := fs.String("out", "", "Output bundle path (required)")
	fs.Parse(os.Args[1:])

	args := fs.Args()
	if *out == "" || len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: --out and at least one cert path are required")
		fmt.Fprintln(os.Stderr, "usage: bridge-ca bundle --out bundle.crt cert1.crt cert2.crt ...")
		os.Exit(1)
	}

	if err := pki.BuildBundle(*out, args...); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Trust bundle: %s\n", *out)
}

func cmdJWTKeygen() {
	fs := flag.NewFlagSet("jwt-keygen", flag.ExitOnError)
	out := fs.String("out", "certs/jwt-signing", "Output base path (creates .key and .pub)")
	fs.Parse(os.Args[1:])

	dir := "."
	base := *out
	if idx := strings.LastIndex(*out, "/"); idx >= 0 {
		dir = (*out)[:idx]
		base = (*out)[idx+1:]
	}

	pubPath, privPath, err := pki.GenerateJWTKeypair(dir, base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Public key:  %s\n", pubPath)
	fmt.Printf("Private key: %s\n", privPath)
}

func cmdVerify() {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	certPath := fs.String("cert", "", "Certificate to verify (required)")
	bundlePath := fs.String("bundle", "", "Trust bundle (required)")
	fs.Parse(os.Args[1:])

	if *certPath == "" || *bundlePath == "" {
		fmt.Fprintln(os.Stderr, "error: --cert and --bundle are required")
		os.Exit(1)
	}

	cert, err := pki.LoadCert(*certPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading cert: %v\n", err)
		os.Exit(1)
	}

	bundlePEM, err := os.ReadFile(*bundlePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading bundle: %v\n", err)
		os.Exit(1)
	}

	pool := pki.NewCertPoolFromPEM(bundlePEM)
	if pool == nil {
		fmt.Fprintln(os.Stderr, "error: failed to parse trust bundle")
		os.Exit(1)
	}

	_, err = cert.Verify(pki.VerifyOpts(pool))
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK: %s verified against bundle\n", *certPath)
}
