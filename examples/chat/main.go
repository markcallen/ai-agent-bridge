// Command chat is a minimal interactive client for the ai-agent-bridge.
// It connects to the bridge and runs a persistent interactive session.
//
// Usage:
//
//	go run ./examples/chat /path/to/repo
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"github.com/google/uuid"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func main() {
	target := flag.String("target", "127.0.0.1:9445", "bridge address")
	project := flag.String("project", "dev", "project ID")
	provider := flag.String("provider", "claude", "provider name")
	timeout := flag.Duration("timeout", 30*time.Second, "RPC timeout")
	sessionIDFlag := flag.String("session-id", "", "reuse an existing session ID (skip StartSession)")

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

	sessionID := *sessionIDFlag
	startedHere := false
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	if *sessionIDFlag == "" {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		_, err = client.StartSession(ctx, &bridgev1.StartSessionRequest{
			ProjectId: *project,
			SessionId: sessionID,
			RepoPath:  repoPath,
			Provider:  *provider,
		})
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "start session: %v\n", err)
			os.Exit(1)
		}
		startedHere = true
	}

	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()

	stream, err := client.StreamEvents(streamCtx, &bridgev1.StreamEventsRequest{
		SessionId: sessionID,
		AfterSeq:  0,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream events: %v\n", err)
		os.Exit(1)
	}

	streamDone := make(chan struct{})
	eventCh := make(chan *bridgev1.SessionEvent, 1024)
	go func() {
		defer close(streamDone)
		defer close(eventCh)
		recvErr := stream.RecvAll(streamCtx, func(ev *bridgev1.SessionEvent) error {
			select {
			case eventCh <- ev:
			default:
			}
			switch ev.Type {
			case bridgev1.EventType_EVENT_TYPE_STDOUT:
				fmt.Printf("< %s\n", ev.Text)
			case bridgev1.EventType_EVENT_TYPE_STDERR:
				fmt.Fprintf(os.Stderr, "< %s\n", ev.Text)
			case bridgev1.EventType_EVENT_TYPE_SESSION_STOPPED:
				fmt.Fprintln(os.Stderr, "session stopped")
				streamCancel()
			case bridgev1.EventType_EVENT_TYPE_SESSION_FAILED:
				if ev.Error != "" {
					fmt.Fprintf(os.Stderr, "session failed: %s\n", ev.Error)
					if strings.Contains(strings.ToLower(ev.Error), "--print") {
						fmt.Fprintln(os.Stderr, "hint: this provider appears to run in one-shot --print mode; use an interactive provider (for example -provider claude-chat or -provider codex) to keep sessions open")
					}
				} else {
					fmt.Fprintln(os.Stderr, "session failed")
				}
				streamCancel()
			}
			return nil
		})
		if recvErr != nil && !errors.Is(recvErr, context.Canceled) {
			fmt.Fprintf(os.Stderr, "recv: %v\n", recvErr)
		}
	}()

	fmt.Fprintf(os.Stderr, "ready (session=%s, Ctrl-C to quit)\n", sessionID)
	fmt.Fprintln(os.Stderr, "commands: /quit, /stop")

	rl, err := readline.NewEx(&readline.Config{
		Prompt:      "> ",
		HistoryFile: "/tmp/ai-agent-bridge-chat.history",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "readline init: %v\n", err)
		if startedHere {
			stopSession(client, sessionID, *timeout)
		}
		streamCancel()
		return
	}
	defer rl.Close()

	for {
		select {
		case <-streamDone:
			return
		default:
		}

		line, err := rl.Readline()
		if err != nil {
			if errors.Is(err, readline.ErrInterrupt) {
				continue
			}
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(os.Stderr)
				if startedHere {
					stopSession(client, sessionID, *timeout)
				}
				streamCancel()
				return
			}
			fmt.Fprintf(os.Stderr, "read: %v\n", err)
			if startedHere {
				stopSession(client, sessionID, *timeout)
			}
			streamCancel()
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch line {
		case "/quit":
			if startedHere {
				stopSession(client, sessionID, *timeout)
			}
			streamCancel()
			return
		case "/stop":
			stopSession(client, sessionID, *timeout)
			streamCancel()
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		resp, err := client.SendInput(ctx, &bridgev1.SendInputRequest{
			SessionId: sessionID,
			Text:      line,
		})
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "send input: %v\n", err)
			continue
		}
		waitForTurn(streamCtx, eventCh, resp.Seq, time.Now().Add(*timeout), 700*time.Millisecond)
	}
}

func stopSession(client *bridgeclient.Client, sessionID string, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if _, err := client.StopSession(ctx, &bridgev1.StopSessionRequest{
		SessionId: sessionID,
	}); err != nil && !errors.Is(err, bridgeclient.ErrSessionNotFound) {
		fmt.Fprintf(os.Stderr, "stop session: %v\n", err)
	}
}

func waitForTurn(ctx context.Context, events <-chan *bridgev1.SessionEvent, afterSeq uint64, deadline time.Time, quietPeriod time.Duration) {
	var idleTimer *time.Timer
	var idleC <-chan time.Time
	gotOutput := false

	for {
		if gotOutput && idleTimer == nil {
			idleTimer = time.NewTimer(quietPeriod)
			idleC = idleTimer.C
		}

		now := time.Now()
		wait := deadline.Sub(now)
		if wait <= 0 {
			if !gotOutput {
				fmt.Fprintln(os.Stderr, "no response before timeout")
			}
			if idleTimer != nil {
				idleTimer.Stop()
			}
			return
		}
		timeout := time.NewTimer(wait)

		select {
		case <-ctx.Done():
			timeout.Stop()
			if idleTimer != nil {
				idleTimer.Stop()
			}
			return
		case <-timeout.C:
			if !gotOutput {
				fmt.Fprintln(os.Stderr, "no response before timeout")
			}
			if idleTimer != nil {
				idleTimer.Stop()
			}
			return
		case <-idleC:
			timeout.Stop()
			return
		case ev, ok := <-events:
			timeout.Stop()
			if !ok {
				if idleTimer != nil {
					idleTimer.Stop()
				}
				return
			}
			switch ev.Type {
			case bridgev1.EventType_EVENT_TYPE_STDOUT, bridgev1.EventType_EVENT_TYPE_STDERR:
				if ev.Seq <= afterSeq {
					continue
				}
				gotOutput = true
				if idleTimer != nil {
					resetTimer(idleTimer, quietPeriod)
				}
			case bridgev1.EventType_EVENT_TYPE_SESSION_STOPPED, bridgev1.EventType_EVENT_TYPE_SESSION_FAILED:
				if idleTimer != nil {
					idleTimer.Stop()
				}
				return
			}
		}
	}
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}
