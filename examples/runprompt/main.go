// Command runprompt starts a new session, runs a single prompt, streams output,
// and exits.
//
// Usage:
//
//	go run ./examples/runprompt [flags] <agent> <repo-path> <prompt>
//
// Example:
//
//	go run ./examples/runprompt \
//	  -target 127.0.0.1:9445 \
//	  claude /path/to/repo "list 5 important TODOs"
package main

import (
	"context"
	"errors"
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
	target := flag.String("target", "127.0.0.1:9445", "bridge gRPC address")
	project := flag.String("project", "dev", "project ID")
	timeout := flag.Duration("timeout", 5*time.Minute, "overall timeout")

	cacert := flag.String("cacert", "", "path to CA bundle")
	cert := flag.String("cert", "", "path to client certificate")
	key := flag.String("key", "", "path to client private key")
	servername := flag.String("servername", "", "TLS server name override")

	jwtKey := flag.String("jwt-key", "", "path to Ed25519 JWT signing key")
	jwtIssuer := flag.String("jwt-issuer", "", "JWT issuer claim")
	jwtAudience := flag.String("jwt-audience", "bridge", "JWT audience claim")

	flag.Parse()

	if flag.NArg() < 3 {
		fmt.Fprintln(os.Stderr, "usage: runprompt [flags] <agent> <repo-path> <prompt>")
		os.Exit(1)
	}

	agent := flag.Arg(0)
	repoPath := flag.Arg(1)
	prompt := strings.Join(flag.Args()[2:], " ")

	opts := []bridgeclient.Option{
		bridgeclient.WithTarget(*target),
		bridgeclient.WithTimeout(*timeout),
	}
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
		fmt.Fprintf(os.Stderr, "failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	client.SetProject(*project)

	code := runPrompt(client, *project, agent, repoPath, prompt, *timeout)
	if code != 0 {
		os.Exit(code)
	}
}

func runPrompt(client *bridgeclient.Client, project, agent, repoPath, prompt string, timeout time.Duration) int {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	sessionID := uuid.NewString()

	_, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId: project,
		SessionId: sessionID,
		RepoPath:  repoPath,
		Provider:  agent,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start session: %v\n", err)
		return 1
	}

	stream, err := client.StreamEvents(ctx, &bridgev1.StreamEventsRequest{
		SessionId: sessionID,
		AfterSeq:  0,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open event stream: %v\n", err)
		return 1
	}

	done := make(chan int, 1)
	var sawOutput bool
	var lastOutputAt time.Time
	var stopRequested bool

	_, err = client.SendInput(ctx, &bridgev1.SendInputRequest{
		SessionId: sessionID,
		Text:      prompt + "\n",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to send input: %v\n", err)
		return 1
	}

	go func() {
		err := stream.RecvAll(ctx, func(ev *bridgev1.SessionEvent) error {
			switch ev.Type {
			case bridgev1.EventType_EVENT_TYPE_STDOUT:
				fmt.Print(ev.Text)
				if strings.TrimSpace(ev.Text) != "" {
					sawOutput = true
					lastOutputAt = time.Now()
				}
			case bridgev1.EventType_EVENT_TYPE_STDERR:
				fmt.Fprint(os.Stderr, ev.Text)
			case bridgev1.EventType_EVENT_TYPE_RESPONSE_COMPLETE:
				// Provider explicitly signaled the response is done — stop the
				// session and return immediately without waiting for idle timeout.
				if sawOutput {
					stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
					_, _ = client.StopSession(stopCtx, &bridgev1.StopSessionRequest{SessionId: sessionID})
					stopCancel()
					done <- 0
				} else {
					done <- 1
				}
				cancel()
			case bridgev1.EventType_EVENT_TYPE_AGENT_READY:
				// Informational only for runprompt.
			case bridgev1.EventType_EVENT_TYPE_SESSION_STOPPED:
				if sawOutput {
					done <- 0
				} else {
					done <- 1
				}
				cancel()
			case bridgev1.EventType_EVENT_TYPE_SESSION_FAILED:
				fmt.Fprintf(os.Stderr, "\nSession FAILED: %s\n", ev.Error)
				done <- 1
				cancel()
			}
			return nil
		})
		if err != nil && !errors.Is(ctx.Err(), context.Canceled) {
			fmt.Fprintf(os.Stderr, "stream error: %v\n", err)
		}
		select {
		case done <- 1:
		default:
		}
	}()

	// Idle-timer fallback for providers that do not emit RESPONSE_COMPLETE.
	idleTicker := time.NewTicker(250 * time.Millisecond)
	defer idleTicker.Stop()
	const idleStopAfter = 2 * time.Second

	select {
	case code := <-done:
		return code
	case <-idleTicker.C:
		for {
			if sawOutput && !stopRequested && !lastOutputAt.IsZero() && time.Since(lastOutputAt) >= idleStopAfter {
				stopRequested = true
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
				_, err := client.StopSession(stopCtx, &bridgev1.StopSessionRequest{SessionId: sessionID})
				stopCancel()
				if err != nil {
					fmt.Fprintf(os.Stderr, "failed to stop session: %v\n", err)
					return 1
				}
			}

			select {
			case code := <-done:
				return code
			case <-ctx.Done():
				fmt.Fprintf(os.Stderr, "timed out after %s\n", timeout)
				return 1
			case <-idleTicker.C:
			}
		}
	case <-ctx.Done():
		fmt.Fprintf(os.Stderr, "timed out after %s\n", timeout)
		return 1
	}
}
