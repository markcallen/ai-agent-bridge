// Command e2e-test connects to the bridge, starts a Claude session against a
// cloned repository, and verifies that it produces non-empty stdout output.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func main() {
	target := flag.String("target", "bridge:9445", "bridge address")
	cacert := flag.String("cacert", "", "CA bundle path")
	cert := flag.String("cert", "", "client cert path")
	key := flag.String("key", "", "client key path")
	jwtKey := flag.String("jwt-key", "", "JWT signing key path")
	jwtIssuer := flag.String("jwt-issuer", "e2e", "JWT issuer")
	repo := flag.String("repo", "/tmp/cache-cleaner", "repo path")
	timeout := flag.Duration("timeout", 2*time.Minute, "overall timeout")
	flag.Parse()

	os.Exit(run(*target, *cacert, *cert, *key, *jwtKey, *jwtIssuer, *repo, *timeout))
}

func run(target, cacert, cert, key, jwtKey, jwtIssuer, repo string, timeout time.Duration) int {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	opts := []bridgeclient.Option{
		bridgeclient.WithTarget(target),
		bridgeclient.WithTimeout(timeout),
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: cacert,
			CertPath:     cert,
			KeyPath:      key,
			ServerName:   "bridge",
		}),
		bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: jwtKey,
			Issuer:         jwtIssuer,
			Audience:       "bridge",
		}),
	}

	client, err := bridgeclient.New(opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: connect: %v\n", err)
		return 1
	}
	defer client.Close()

	sessionID := uuid.NewString()
	project := "e2e"
	client.SetProject(project)

	fmt.Printf("Starting session %s (repo=%s)...\n", sessionID, repo)

	_, err = client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId: project,
		SessionId: sessionID,
		RepoPath:  repo,
		Provider:  "claude",
		AgentOpts: map[string]string{
			"arg:prompt": "What language is this project written in?",
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: start session: %v\n", err)
		return 1
	}

	stream, err := client.StreamEvents(ctx, &bridgev1.StreamEventsRequest{
		SessionId: sessionID,
		AfterSeq:  0,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: stream events: %v\n", err)
		return 1
	}

	var stdout strings.Builder
	done := make(chan int, 1)

	go func() {
		err := stream.RecvAll(ctx, func(ev *bridgev1.SessionEvent) error {
			switch ev.Type {
			case bridgev1.EventType_EVENT_TYPE_STDOUT:
				fmt.Print(ev.Text)
				stdout.WriteString(ev.Text)
			case bridgev1.EventType_EVENT_TYPE_STDERR:
				fmt.Fprint(os.Stderr, ev.Text)
			case bridgev1.EventType_EVENT_TYPE_SESSION_STOPPED:
				fmt.Println("\nSession stopped.")
				if strings.TrimSpace(stdout.String()) == "" {
					done <- 1
				} else {
					done <- 0
				}
				cancel()
			case bridgev1.EventType_EVENT_TYPE_SESSION_FAILED:
				fmt.Fprintf(os.Stderr, "\nSession FAILED: %s\n", ev.Error)
				done <- 1
				cancel()
			}
			return nil
		})
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "ERROR: recv: %v\n", err)
		}
		select {
		case done <- 1:
		default:
		}
	}()

	select {
	case code := <-done:
		return code
	case <-ctx.Done():
		fmt.Fprintf(os.Stderr, "ERROR: timed out after %s\n", timeout)
		return 1
	}
}
