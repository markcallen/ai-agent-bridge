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
	target := flag.String("target", "127.0.0.1:9445", "bridge address")
	provider := flag.String("provider", "fixture", "provider name to use")
	repoPath := flag.String("repo-path", "/workspace/smoke-repo", "repo path for session")
	timeout := flag.Duration("timeout", 60*time.Second, "overall timeout")
	flag.Parse()

	client, err := bridgeclient.New(
		bridgeclient.WithTarget(*target),
		bridgeclient.WithTimeout(10*time.Second),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "PROFILE SMOKE FAILED: connect: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = client.Close() }()

	deadline := time.Now().Add(*timeout)

	// Wait for bridge to be healthy.
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		h, hErr := client.Health(ctx)
		cancel()
		if hErr == nil && h.GetStatus() == "serving" {
			break
		}
		time.Sleep(time.Second)
	}

	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	health, err := client.Health(ctx)
	if err != nil || health.GetStatus() != "serving" {
		fmt.Fprintf(os.Stderr, "PROFILE SMOKE FAILED: bridge not healthy (err=%v status=%s)\n", err, health.GetStatus())
		os.Exit(1)
	}

	// Verify the fixture provider is registered.
	providers, err := client.ListProviders(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "PROFILE SMOKE FAILED: list providers: %v\n", err)
		os.Exit(1)
	}
	found := false
	for _, p := range providers.GetProviders() {
		if p.GetProvider() == *provider {
			found = true
		}
	}
	if !found {
		var ids []string
		for _, p := range providers.GetProviders() {
			ids = append(ids, p.GetProvider())
		}
		fmt.Fprintf(os.Stderr, "PROFILE SMOKE FAILED: provider %q not registered; got %v\n", *provider, ids)
		os.Exit(1)
	}

	// Start a session against the workspace repo path.
	sessionID := uuid.NewString()
	_, err = client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId: "smoke",
		SessionId: sessionID,
		RepoPath:  *repoPath,
		Provider:  *provider,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "PROFILE SMOKE FAILED: start session: %v\n", err)
		os.Exit(1)
	}

	// Attach and collect output in a goroutine.
	clientID := uuid.NewString()
	outputCh := make(chan string, 32)
	errCh := make(chan error, 1)
	attachCtx, attachCancel := context.WithDeadline(context.Background(), deadline)
	defer attachCancel()

	stream, err := client.AttachSession(attachCtx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  clientID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "PROFILE SMOKE FAILED: attach: %v\n", err)
		os.Exit(1)
	}

	go func() {
		recvErr := stream.RecvAll(attachCtx, func(ev *bridgev1.AttachSessionEvent) error {
			if len(ev.GetPayload()) > 0 {
				outputCh <- string(ev.GetPayload())
			}
			return nil
		})
		if recvErr != nil && !errors.Is(recvErr, context.Canceled) && !errors.Is(recvErr, context.DeadlineExceeded) {
			errCh <- recvErr
		}
		close(errCh)
	}()

	// Give the session a moment to start.
	time.Sleep(500 * time.Millisecond)

	// Send a probe string and wait for the echo.
	probe := "bridge-smoke-echo-test"
	_, err = client.WriteInput(ctx, &bridgev1.WriteInputRequest{
		SessionId: sessionID,
		ClientId:  clientID,
		Data:      []byte(probe + "\n"),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "PROFILE SMOKE FAILED: write input: %v\n", err)
		os.Exit(1)
	}

	// Collect output until we see the probe echoed back.
	echoDeadline := time.Now().Add(10 * time.Second)
	var collected strings.Builder
	for time.Now().Before(echoDeadline) {
		select {
		case chunk, ok := <-outputCh:
			if !ok {
				goto echoCheck
			}
			collected.WriteString(chunk)
			if strings.Contains(collected.String(), probe) {
				goto echoCheck
			}
		case recvErr, ok := <-errCh:
			if ok && recvErr != nil {
				fmt.Fprintf(os.Stderr, "PROFILE SMOKE FAILED: stream error: %v\n", recvErr)
				os.Exit(1)
			}
			goto echoCheck
		case <-time.After(100 * time.Millisecond):
		}
	}

echoCheck:
	if !strings.Contains(collected.String(), probe) {
		fmt.Fprintf(os.Stderr, "PROFILE SMOKE FAILED: probe %q not echoed; got: %q\n", probe, collected.String())
		os.Exit(1)
	}

	attachCancel()

	fmt.Printf("PROFILE SMOKE PASSED: provider=%s repo_path=%s providers=%d echo=ok\n",
		*provider, *repoPath, len(providers.GetProviders()))
}
