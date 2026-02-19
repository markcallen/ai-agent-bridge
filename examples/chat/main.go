// Command chat is an interactive prompt loop that sends each input to the
// bridge as a new claude --print session and streams the response back.
//
// Usage:
//
//	go run ./examples/chat [flags] <repo-path>
//
// Example:
//
//	go run ./examples/chat -target 127.0.0.1:9445 \
//	  -provider claude \
//	  /path/to/repo
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"github.com/google/uuid"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func main() {
	target := flag.String("target", "127.0.0.1:9445", "bridge gRPC address")
	project := flag.String("project", "dev", "project ID")
	provider := flag.String("provider", "claude", "provider name (e.g. echo, claude)")
	timeout := flag.Duration("timeout", 5*time.Minute, "per-prompt timeout")

	cacert := flag.String("cacert", "", "path to CA bundle")
	cert := flag.String("cert", "", "path to client certificate")
	key := flag.String("key", "", "path to client private key")
	servername := flag.String("servername", "", "TLS server name override")

	jwtKey := flag.String("jwt-key", "", "path to Ed25519 JWT signing key")
	jwtIssuer := flag.String("jwt-issuer", "", "JWT issuer claim")
	jwtAudience := flag.String("jwt-audience", "bridge", "JWT audience claim")

	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: chat [flags] <repo-path>")
		os.Exit(1)
	}
	repoPath := flag.Arg(0)

	// Build client options.
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

	// Setup readline.
	rl, err := readline.NewEx(&readline.Config{
		Prompt:      "you> ",
		HistoryFile: "/tmp/ai-agent-bridge-chat.history",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "readline: %v\n", err)
		os.Exit(1)
	}
	defer rl.Close()

	// Handle Ctrl-C.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\nGoodbye!")
		rl.Close()
		os.Exit(0)
	}()

	fmt.Fprintln(os.Stderr, "Type a prompt and press Enter. Type /quit to exit.")
	fmt.Fprintln(os.Stderr, "Each prompt starts a new agent session.")
	fmt.Fprintln(os.Stderr, "---")

	for {
		line, err := rl.Readline()
		if err != nil {
			if errors.Is(err, readline.ErrInterrupt) || errors.Is(err, io.EOF) {
				break
			}
			fmt.Fprintf(os.Stderr, "read error: %v\n", err)
			break
		}

		prompt := strings.TrimSpace(line)
		if prompt == "" {
			continue
		}
		if prompt == "/quit" {
			break
		}

		code := runPrompt(client, *project, *provider, repoPath, prompt, *timeout)
		fmt.Println() // blank line after response
		if code != 0 {
			fmt.Fprintln(os.Stderr, "(session returned an error)")
		}
	}

	fmt.Fprintln(os.Stderr, "Goodbye!")
}

// runPrompt starts a new session with the given prompt, streams the response,
// and returns 0 on success or 1 on failure.
func runPrompt(client *bridgeclient.Client, project, provider, repoPath, prompt string, timeout time.Duration) int {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	sessionID := uuid.NewString()

	_, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId: project,
		SessionId: sessionID,
		RepoPath:  repoPath,
		Provider:  provider,
		AgentOpts: map[string]string{
			"arg:prompt": prompt,
		},
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
			fmt.Fprintf(os.Stderr, "stream error: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "timed out after %s\n", timeout)
		return 1
	}
}
