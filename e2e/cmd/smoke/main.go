package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func main() {
	target := flag.String("target", "127.0.0.1:9445", "bridge address")
	cacert := flag.String("cacert", "", "CA bundle path")
	cert := flag.String("cert", "", "client cert path")
	key := flag.String("key", "", "client key path")
	jwtKey := flag.String("jwt-key", "", "JWT signing key path")
	timeout := flag.Duration("timeout", 90*time.Second, "overall timeout")
	flag.Parse()

	client, err := bridgeclient.New(
		bridgeclient.WithTarget(*target),
		bridgeclient.WithTimeout(10*time.Second),
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: *cacert,
			CertPath:     *cert,
			KeyPath:      *key,
			ServerName:   "bridge.local",
		}),
		bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: *jwtKey,
			Issuer:         "dev",
			Audience:       "bridge",
		}),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SMOKE TEST FAILED: connect: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = client.Close()
	}()
	client.SetProject("smoke")

	deadline := time.Now().Add(*timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		health, healthErr := client.Health(ctx)
		if healthErr == nil && health.GetStatus() == "serving" {
			providers, providersErr := client.ListProviders(ctx)
			cancel()
			if providersErr != nil {
				fmt.Fprintf(os.Stderr, "SMOKE TEST FAILED: list providers: %v\n", providersErr)
				os.Exit(1)
			}
			fmt.Printf("SMOKE TEST PASSED: status=%s providers=%d\n", health.GetStatus(), len(providers.GetProviders()))
			return
		}
		cancel()
		time.Sleep(2 * time.Second)
	}

	fmt.Fprintln(os.Stderr, "SMOKE TEST FAILED: timed out waiting for healthy bridge")
	os.Exit(1)
}
