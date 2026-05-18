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
	timeout := flag.Duration("timeout", 10*time.Second, "health check timeout")
	flag.Parse()

	client, err := bridgeclient.New(
		bridgeclient.WithTarget(*target),
		bridgeclient.WithTimeout(*timeout),
	)
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
