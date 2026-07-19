package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/markcallen/ai-agent-bridge/internal/localserver"
)

func newServerInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize server configuration",
		Long: `Interactively create or update ~/.ai-agent-bridge/bridge.yaml.

Asks for listen address, server SANs (auto-detects Tailscale FQDN),
and optional Step CA integration. The generated config is used
automatically by 'bridgectl server start'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServerInit()
		},
	}
	return cmd
}

func runServerInit() error {
	stateDir := localserver.StateDir()
	cfgPath := filepath.Join(stateDir, "bridge.yaml")
	certsDir := filepath.Join(stateDir, "certs")

	// Load existing config if present.
	type existingConfig struct {
		Server struct {
			Listen string   `yaml:"listen"`
			SANs   []string `yaml:"san"`
		} `yaml:"server"`
		StepCA struct {
			URL                     string `yaml:"url"`
			Root                    string `yaml:"root"`
			Provisioner             string `yaml:"provisioner"`
			ProvisionerPasswordFile string `yaml:"provisioner_password_file"`
		} `yaml:"step_ca"`
	}
	existing := &existingConfig{}
	if data, err := os.ReadFile(cfgPath); err == nil {
		_ = yaml.Unmarshal(data, existing)
	}

	reader := bufio.NewReader(os.Stdin)

	// 1. Listen address.
	listenDefault := "0.0.0.0:9445"
	if existing.Server.Listen != "" {
		listenDefault = existing.Server.Listen
	}
	listen := prompt(reader, "Listen address", listenDefault)

	// 2. Server SANs — auto-detect Tailscale FQDN.
	var sanDefault string
	if len(existing.Server.SANs) > 0 {
		sanDefault = strings.Join(existing.Server.SANs, ",")
	}
	if sanDefault == "" {
		if fqdn := detectTailscaleFQDN(); fqdn != "" {
			sanDefault = fqdn
		}
	}
	sanInput := prompt(reader, "Server SANs (comma-separated hostnames/IPs)", sanDefault)
	var sans []string
	for _, s := range strings.Split(sanInput, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			sans = append(sans, s)
		}
	}

	// 3. Step CA.
	type stepCASettings struct {
		URL                     string
		Root                    string
		Provisioner             string
		ProvisionerPasswordFile string
	}
	var stepCA stepCASettings
	useStepCA := promptYesNo(reader, "Use Step CA for PKI?", existing.StepCA.URL != "")

	if useStepCA {
		urlDefault := existing.StepCA.URL
		stepCA.URL = prompt(reader, "Step CA URL", urlDefault)

		// Auto-fetch root cert.
		rootPath := filepath.Join(certsDir, "step-ca-root.crt")
		if existing.StepCA.Root != "" {
			rootPath = existing.StepCA.Root
		}

		fmt.Printf("Fetching root cert from %s...\n", stepCA.URL)
		if err := fetchStepCARoot(stepCA.URL, rootPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not fetch root cert: %v\n", err)
			rootPath = prompt(reader, "Path to Step CA root certificate", rootPath)
		} else {
			fmt.Printf("  Saved to %s\n", rootPath)
		}
		stepCA.Root = rootPath

		provDefault := existing.StepCA.Provisioner
		if provDefault == "" {
			provDefault = "acme"
		}
		stepCA.Provisioner = prompt(reader, "Provisioner name", provDefault)

		if stepCA.Provisioner != "acme" {
			pwFile := prompt(reader, "Provisioner password file (leave empty for interactive)", existing.StepCA.ProvisionerPasswordFile)
			stepCA.ProvisionerPasswordFile = pwFile
		}
	}

	// Build a minimal YAML config with only the fields we collected.
	out := make(map[string]interface{})

	server := map[string]interface{}{"listen": listen}
	if len(sans) > 0 {
		server["san"] = sans
	}
	out["server"] = server

	if useStepCA {
		sc := map[string]interface{}{
			"url":         stepCA.URL,
			"root":        stepCA.Root,
			"provisioner": stepCA.Provisioner,
		}
		if stepCA.ProvisionerPasswordFile != "" {
			sc["provisioner_password_file"] = stepCA.ProvisionerPasswordFile
		}
		out["step_ca"] = sc
	}

	data, err := yaml.Marshal(out)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	// Write.
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	if err := os.MkdirAll(certsDir, 0o700); err != nil {
		return fmt.Errorf("create certs dir: %w", err)
	}
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Printf("\nWrote %s\n", cfgPath)
	fmt.Println("\nStart the server with:")
	fmt.Println("  bridgectl server start")
	return nil
}

// prompt prints a question and returns the user's input, or the default.
func prompt(reader *bufio.Reader, question, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", question, defaultVal)
	} else {
		fmt.Printf("%s: ", question)
	}
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}

// promptYesNo asks a yes/no question.
func promptYesNo(reader *bufio.Reader, question string, defaultYes bool) bool {
	hint := "y/N"
	if defaultYes {
		hint = "Y/n"
	}
	fmt.Printf("%s [%s]: ", question, hint)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
}

// detectTailscaleFQDN tries to get the machine's Tailscale FQDN.
func detectTailscaleFQDN() string {
	out, err := exec.Command("tailscale", "status", "--json").Output()
	if err != nil {
		return ""
	}
	var status struct {
		Self struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return ""
	}
	// DNSName has a trailing dot; remove it.
	return strings.TrimSuffix(status.Self.DNSName, ".")
}

// fetchStepCARoot fetches the root certificate from a Step CA /roots endpoint.
// verifyStepCARoot dials the Step CA using the PEM root at rootPath and returns
// an error if the TLS handshake fails. This lets callers detect a stale cached
// root and re-fetch before proceeding.
func verifyStepCARoot(caURL, rootPath string) error {
	pemData, err := os.ReadFile(rootPath)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	block, _ := pem.Decode(pemData)
	if block == nil {
		return fmt.Errorf("no PEM block in %s", rootPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	pool.AddCert(cert)

	u, err := url.Parse(caURL)
	if err != nil {
		return err
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "443"
	}

	conn, err := tls.Dial("tcp", host+":"+port, &tls.Config{
		RootCAs:    pool,
		ServerName: host,
	})
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

func fetchStepCARoot(caURL, outPath string) error {
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // bootstrapping trust
		},
	}
	resp, err := client.Get(caURL + "/roots")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}

	var roots struct {
		Crts []string `json:"crts"`
	}
	if err := json.Unmarshal(body, &roots); err != nil {
		return fmt.Errorf("parse /roots response: %w", err)
	}
	if len(roots.Crts) == 0 {
		return fmt.Errorf("no certificates in /roots response")
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(outPath, []byte(roots.Crts[0]), 0o644)
}
