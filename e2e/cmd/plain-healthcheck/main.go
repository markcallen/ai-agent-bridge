package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/markcallen/ai-agent-bridge/internal/localserver"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func main() {
	target := flag.String("target", "", "bridge address (empty = auto-discover from state dir)")
	stateDir := flag.String("state-dir", "", "bridge state directory (empty = default)")
	timeout := flag.Duration("timeout", 10*time.Second, "health check timeout")
	flag.Parse()

	var client *bridgeclient.Client
	var err error

	if *target != "" {
		// Explicit target: connect insecurely (backwards-compatible behaviour).
		client, err = bridgeclient.New(
			bridgeclient.WithTarget(*target),
			bridgeclient.WithTimeout(*timeout),
		)
	} else {
		// Auto-discover: use DiscoverTarget + mode-appropriate credentials.
		sd := *stateDir
		if sd == "" {
			sd = localserver.StateDir()
		}
		addr, mode := localserver.DiscoverTarget(sd)
		if addr == "" {
			fmt.Fprintf(os.Stderr, "HEALTHCHECK FAILED: no bridge server found in %s\n", sd)
			os.Exit(1)
		}
		opts := []bridgeclient.Option{
			bridgeclient.WithTarget(addr),
			bridgeclient.WithTimeout(*timeout),
		}
		if mode == localserver.ModeSecure {
			mat := localserver.LoadPKIMaterial(sd)
			opts = append(opts,
				bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
					CABundlePath: mat.CABundlePath,
					CertPath:     mat.LocalClientCert,
					KeyPath:      mat.LocalClientKey,
					ServerName:   "server",
				}),
				bridgeclient.WithJWT(bridgeclient.JWTConfig{
					PrivateKeyPath: mat.JWTSigningKey,
					Issuer:         "local",
					Audience:       "bridge",
					TTL:            5 * time.Minute,
				}),
			)
		}
		client, err = bridgeclient.New(opts...)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "HEALTHCHECK FAILED: connect: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = client.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	resp, err := client.Health(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "HEALTHCHECK FAILED: %v\n", err)
		os.Exit(1)
	}
	if resp.GetStatus() != "serving" {
		fmt.Fprintf(os.Stderr, "HEALTHCHECK FAILED: status=%s\n", resp.GetStatus())
		os.Exit(1)
	}

	fmt.Printf("HEALTHCHECK PASSED: status=%s providers=%d\n", resp.GetStatus(), len(resp.GetProviders()))
}
