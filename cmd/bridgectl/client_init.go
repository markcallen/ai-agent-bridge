package main

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/markcallen/ai-agent-bridge/internal/localserver"
)

func newClientInitCmd() *cobra.Command {
	var (
		stepCAURL               string
		rootPath                string
		provisioner             string
		provisionerPasswordFile string
		name                    string
		target                  string
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Obtain a client certificate from Step CA",
		Long: `Obtain a client certificate from a Step CA instance and optionally
enroll with a bridge server in one step.

Discovers available provisioners from the CA and lets you choose.
JWK provisioners authenticate with a password (no port binding needed).
ACME provisioners use HTTP-01 challenge on port 80 (requires root/sudo).

After obtaining the certificate, pass --target to automatically enroll
the JWT key with a bridge server.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClientInit(stepCAURL, rootPath, provisioner, provisionerPasswordFile, name, target)
		},
	}

	cmd.Flags().StringVar(&stepCAURL, "step-ca-url", "", "Step CA server URL (e.g. https://step-ca-dev.example.com)")
	cmd.Flags().StringVar(&rootPath, "step-ca-root", "", "path to Step CA root certificate (auto-fetched if not set)")
	cmd.Flags().StringVar(&provisioner, "provisioner", "", "provisioner name (skip discovery)")
	cmd.Flags().StringVar(&provisionerPasswordFile, "step-ca-provisioner-password-file", "", "path to JWK provisioner password file")
	cmd.Flags().StringVar(&name, "name", "", "client name for the certificate CN (default: hostname)")
	cmd.Flags().StringVar(&target, "target", "", "bridge server address to auto-enroll after cert issuance (e.g. host or host:9445; default port 9445)")

	return cmd
}

// caProvisioner holds a provisioner entry from the Step CA /provisioners API.
type caProvisioner struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// discoverProvisioners fetches the provisioner list from a Step CA server.
func discoverProvisioners(caURL string) ([]caProvisioner, error) {
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // bootstrapping trust
		},
		Timeout: 10 * time.Second,
	}
	resp, err := client.Get(caURL + "/provisioners?limit=100")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Provisioners []caProvisioner `json:"provisioners"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse provisioners: %w", err)
	}
	return result.Provisioners, nil
}

func runClientInit(stepCAURL, rootPath, provisioner, provisionerPasswordFile, name, target string) error {
	stepCAURL = strings.TrimRight(stepCAURL, "/")

	stateDir := localserver.StateDir()
	certsDir := filepath.Join(stateDir, "certs")

	if err := os.MkdirAll(certsDir, 0o700); err != nil {
		return fmt.Errorf("create certs dir: %w", err)
	}

	reader := bufio.NewReader(os.Stdin)

	// Step CA URL.
	if stepCAURL == "" {
		stepCAURL = strings.TrimRight(prompt(reader, "Step CA URL", ""), "/")
		if stepCAURL == "" {
			return fmt.Errorf("--step-ca-url is required")
		}
	}

	// Root cert — fetch if not provided, or if the cached copy no longer validates the CA.
	if rootPath == "" {
		rootPath = filepath.Join(certsDir, "step-ca-root.crt")
		if _, err := os.Stat(rootPath); os.IsNotExist(err) {
			fmt.Printf("Fetching root cert from %s...\n", stepCAURL)
			if err := fetchStepCARoot(stepCAURL, rootPath); err != nil {
				return fmt.Errorf("fetch Step CA root cert: %w", err)
			}
			fmt.Printf("  Saved to %s\n", rootPath)
		} else if err := verifyStepCARoot(stepCAURL, rootPath); err != nil {
			fmt.Printf("Cached root cert no longer valid (%v), re-fetching...\n", err)
			if err := fetchStepCARoot(stepCAURL, rootPath); err != nil {
				return fmt.Errorf("fetch Step CA root cert: %w", err)
			}
			fmt.Printf("  Updated root cert saved to %s\n", rootPath)
		} else {
			fmt.Printf("Using existing root cert: %s\n", rootPath)
		}
	}

	// Client name.
	if name == "" {
		hostname, _ := os.Hostname()
		name = prompt(reader, "Client name", hostname)
	}

	// Provisioner — discover from CA if not specified.
	if provisioner == "" {
		fmt.Printf("Discovering provisioners from %s...\n", stepCAURL)
		provs, err := discoverProvisioners(stepCAURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not discover provisioners: %v\n", err)
			provisioner = prompt(reader, "Provisioner name", "")
		} else if len(provs) == 0 {
			return fmt.Errorf("no provisioners found on %s", stepCAURL)
		} else if len(provs) == 1 {
			provisioner = provs[0].Name
			fmt.Printf("  Using provisioner: %s (%s)\n", provs[0].Name, provs[0].Type)
		} else {
			fmt.Println("  Available provisioners:")
			for i, p := range provs {
				note := ""
				if strings.EqualFold(p.Type, "JWK") {
					note = " (password — no sudo needed)"
				} else if strings.EqualFold(p.Type, "ACME") {
					note = " (HTTP-01 — needs sudo for port 80)"
				}
				fmt.Printf("    %d. %s [%s]%s\n", i+1, p.Name, p.Type, note)
			}
			choice := prompt(reader, "Choose provisioner", provs[0].Name)
			// Accept a number or a name.
			provisioner = choice
			for i, p := range provs {
				if choice == fmt.Sprintf("%d", i+1) {
					provisioner = p.Name
					break
				}
			}
		}
	}

	if provisioner == "" {
		return fmt.Errorf("provisioner name is required")
	}

	certPath := filepath.Join(certsDir, name+".crt")
	keyPath := filepath.Join(certsDir, name+".key")

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	stepCfg := &localserver.StepCAConfig{
		URL:                     stepCAURL,
		RootPath:                rootPath,
		Provisioner:             provisioner,
		ProvisionerPasswordFile: provisionerPasswordFile,
	}

	isACME := strings.EqualFold(provisioner, "acme")

	if isACME {
		fmt.Println("\nUsing ACME provisioner (HTTP-01 challenge on port 80 — may need sudo)")
		if err := localserver.RequestCertACME(stepCfg, []string{name}, certPath, keyPath, logger); err != nil {
			if strings.Contains(err.Error(), "permission denied") || strings.Contains(err.Error(), "bind") {
				fmt.Fprintf(os.Stderr, "\nPort 80 requires elevated privileges. Re-run with sudo:\n")
				fmt.Fprintf(os.Stderr, "  sudo bridgectl client init --step-ca-url %s --provisioner acme\n", stepCAURL)
			}
			return fmt.Errorf("obtain client cert via ACME: %w", err)
		}
	} else {
		fmt.Printf("\nUsing JWK provisioner %q\n", provisioner)
		if err := localserver.RequestCertJWK(stepCfg, []string{name}, certPath, keyPath, logger); err != nil {
			return fmt.Errorf("obtain client cert via JWK: %w", err)
		}
	}

	fmt.Printf("\nClient certificate saved:\n")
	fmt.Printf("  Cert: %s\n", certPath)
	fmt.Printf("  Key:  %s\n", keyPath)

	// Auto-enroll if --target was provided.
	if target != "" {
		target = normalizeRemoteTarget(target)
		fmt.Printf("\nEnrolling with %s...\n", target)
		enrollArgs := []string{
			"enroll",
			"--target", target,
			"--ca", rootPath,
			"--cert", certPath,
			"--key", keyPath,
			"--name", name,
			"--out", certsDir,
		}
		enrollCmd := newClientEnrollCmd()
		enrollCmd.SilenceUsage = true
		enrollCmd.SetArgs(enrollArgs)
		if err := enrollCmd.Execute(); err != nil {
			return fmt.Errorf("auto-enroll: %w", err)
		}
	} else {
		fmt.Println("\nNext step — enroll with a bridge server:")
		fmt.Printf("  bridgectl client enroll \\\n")
		fmt.Printf("    --target <host>:9445 \\\n")
		fmt.Printf("    --ca %s \\\n", rootPath)
		fmt.Printf("    --cert %s \\\n", certPath)
		fmt.Printf("    --key %s\n", keyPath)
	}

	return nil
}
