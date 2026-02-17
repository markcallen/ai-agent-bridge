// Command chat is a minimal interactive client for the ai-agent-bridge.
// It connects to the bridge and runs one Claude print session per prompt.
//
// Usage:
//
//	go run ./examples/chat /path/to/repo
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func main() {
	target := flag.String("target", "127.0.0.1:9445", "bridge address")
	project := flag.String("project", "dev", "project ID")
	provider := flag.String("provider", "claude", "provider name")
	timeout := flag.Duration("timeout", 2*time.Minute, "per-prompt timeout")

	cacert := flag.String("cacert", "", "CA certificate for mTLS")
	cert := flag.String("cert", "", "client certificate for mTLS")
	key := flag.String("key", "", "client key for mTLS")
	servername := flag.String("servername", "", "TLS server name override")

	jwtKey := flag.String("jwt-key", "", "JWT signing key path")
	jwtIssuer := flag.String("jwt-issuer", "", "JWT issuer")
	jwtAudience := flag.String("jwt-audience", "bridge", "JWT audience")

	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: chat [flags] <repo-path>")
		os.Exit(1)
	}
	repoPath := flag.Arg(0)

	// Build client options.
	opts := []bridgeclient.Option{bridgeclient.WithTarget(*target)}

	if *cacert != "" && *cert != "" && *key != "" {
		opts = append(opts, bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: *cacert,
			CertPath:     *cert,
			KeyPath:      *key,
			ServerName:   *servername,
		}))
	}

	if *jwtKey != "" {
		opts = append(opts, bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: *jwtKey,
			Issuer:         *jwtIssuer,
			Audience:       *jwtAudience,
		}))
	}

	client, err := bridgeclient.New(opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	client.SetProject(*project)

	fmt.Fprintf(os.Stderr, "ready (Ctrl-C to quit)\n")

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Fprint(os.Stderr, "> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(os.Stderr)
				return
			}
			fmt.Fprintf(os.Stderr, "read: %v\n", err)
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		sessionID := uuid.NewString()
		fmt.Fprintf(os.Stderr, "[session %s] %s\n", sessionID, repoPath)

		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		_, err = client.StartSession(ctx, &bridgev1.StartSessionRequest{
			ProjectId: *project,
			SessionId: sessionID,
			RepoPath:  repoPath,
			Provider:  *provider,
			AgentOpts: map[string]string{
				"arg:prompt": line,
			},
		})
		if err != nil {
			cancel()
			fmt.Fprintf(os.Stderr, "start session: %v\n", err)
			continue
		}

		stream, err := client.StreamEvents(ctx, &bridgev1.StreamEventsRequest{
			SessionId: sessionID,
			AfterSeq:  0,
		})
		if err != nil {
			cancel()
			fmt.Fprintf(os.Stderr, "stream events: %v\n", err)
			continue
		}

		failed := false
		err = stream.RecvAll(ctx, func(ev *bridgev1.SessionEvent) error {
			switch ev.Type {
			case bridgev1.EventType_EVENT_TYPE_STDOUT:
				fmt.Printf("< %s\n", ev.Text)
			case bridgev1.EventType_EVENT_TYPE_STDERR:
				fmt.Fprintf(os.Stderr, "< %s\n", ev.Text)
			case bridgev1.EventType_EVENT_TYPE_SESSION_STOPPED:
				cancel()
			case bridgev1.EventType_EVENT_TYPE_SESSION_FAILED:
				failed = true
				if ev.Error != "" {
					fmt.Fprintf(os.Stderr, "session failed: %s\n", ev.Error)
				}
				cancel()
			}
			return nil
		})
		cancel()

		if err != nil && !failed && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(os.Stderr, "recv: %v\n", err)
		}
	}
}
