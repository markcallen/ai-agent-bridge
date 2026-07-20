package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func main() {
	target := flag.String("target", "macbook.tail6198c2.ts.net:9445", "bridge gRPC address")
	project := flag.String("project", "", "filter by project ID (empty = all projects)")

	caBundle := flag.String("cacert", "/home/marka/.ai-agent-bridge/certs/step-ca-root.crt", "path to CA bundle")
	cert := flag.String("cert", "/home/marka/.ai-agent-bridge/certs/do-dev2.crt", "path to client certificate")
	key := flag.String("key", "/home/marka/.ai-agent-bridge/certs/do-dev2.key", "path to client private key")

	jwtKey := flag.String("jwt-key", "jwt-signing.key", "path to Ed25519 JWT signing key")
	jwtIssuer := flag.String("jwt-issuer", "do-dev2", "JWT issuer claim")

	flag.Parse()

	client, err := bridgeclient.New(
		bridgeclient.WithTarget(*target),
		bridgeclient.WithTimeout(10*time.Second),
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: *caBundle,
			CertPath:     *cert,
			KeyPath:      *key,
			ServerName:   "server",
		}),
		bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: *jwtKey,
			Issuer:         *jwtIssuer,
			Audience:       "bridge",
		}),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	resp, err := client.ListSessions(ctx, &bridgev1.ListSessionsRequest{
		ProjectId: *project,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "list sessions: %v\n", err)
		os.Exit(1)
	}

	if len(resp.Sessions) == 0 {
		fmt.Println("no sessions")
		return
	}

	fmt.Printf("%-38s  %-12s  %-10s  %-10s  %s\n", "SESSION ID", "PROJECT", "PROVIDER", "STATUS", "CREATED")
	fmt.Println(strings.Repeat("-", 100))
	for _, s := range resp.Sessions {
		status := strings.TrimPrefix(s.Status.String(), "SESSION_STATUS_")
		created := ""
		if s.CreatedAt != nil {
			created = s.CreatedAt.AsTime().Local().Format("15:04:05")
		}
		fmt.Printf("%-38s  %-12s  %-10s  %-10s  %s\n",
			s.SessionId, s.ProjectId, s.Provider, status, created)
	}
}
