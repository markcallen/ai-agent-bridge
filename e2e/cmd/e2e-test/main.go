// Command e2e-test connects to the bridge, starts a Claude session against a
// cloned repository, and verifies that it produces non-empty stdout output.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
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
	only := flag.String("only", "all", "test subset: all, chat-sdk, chat-cli, chat")
	flag.Parse()

	os.Exit(run(*target, *cacert, *cert, *key, *jwtKey, *jwtIssuer, *repo, *timeout, *only))
}

func run(target, cacert, cert, key, jwtKey, jwtIssuer, repo string, timeout time.Duration, only string) int {
	baseMTLS := bridgeclient.MTLSConfig{
		CABundlePath: cacert,
		CertPath:     cert,
		KeyPath:      key,
		ServerName:   "bridge",
	}
	baseJWT := bridgeclient.JWTConfig{
		PrivateKeyPath: jwtKey,
		Issuer:         jwtIssuer,
		Audience:       "bridge",
	}

	newStepContext := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), timeout)
	}

	ctx, cancel := newStepContext()
	if err := runMTLSRejectionScenarios(ctx, target, timeout, baseMTLS, baseJWT); err != nil {
		cancel()
		fmt.Fprintf(os.Stderr, "ERROR: mTLS rejection scenarios failed: %v\n", err)
		return 1
	}
	cancel()

	ctx, cancel = newStepContext()
	if err := runJWTRejectionScenarios(ctx, target, timeout, baseMTLS, jwtKey, jwtIssuer); err != nil {
		cancel()
		fmt.Fprintf(os.Stderr, "ERROR: JWT rejection scenarios failed: %v\n", err)
		return 1
	}
	cancel()

	client, err := bridgeclient.New(
		bridgeclient.WithTarget(target),
		bridgeclient.WithTimeout(timeout),
		bridgeclient.WithMTLS(baseMTLS),
		bridgeclient.WithJWT(baseJWT),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: connect: %v\n", err)
		return 1
	}
	defer client.Close()

	project := "e2e"
	client.SetProject(project)

	switch only {
	case "chat-sdk":
		ctx, cancel = newStepContext()
		if err := runChatExampleTest(ctx, client, repo); err != nil {
			cancel()
			fmt.Fprintf(os.Stderr, "ERROR: chat example test: %v\n", err)
			return 1
		}
		cancel()
		return 0
	case "chat-cli":
		if err := runChatExampleCLIE2E(target, cacert, cert, key, jwtKey, jwtIssuer, repo); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: chat example CLI e2e: %v\n", err)
			return 1
		}
		return 0
	case "chat":
		ctx, cancel = newStepContext()
		if err := runChatExampleTest(ctx, client, repo); err != nil {
			cancel()
			fmt.Fprintf(os.Stderr, "ERROR: chat example test: %v\n", err)
			return 1
		}
		cancel()
		if err := runChatExampleCLIE2E(target, cacert, cert, key, jwtKey, jwtIssuer, repo); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: chat example CLI e2e: %v\n", err)
			return 1
		}
		return 0
	case "all":
		// continue with full suite below
	default:
		fmt.Fprintf(os.Stderr, "ERROR: unknown -only value %q (valid: all, chat-sdk, chat-cli, chat)\n", only)
		return 1
	}

	ctx, cancel = newStepContext()
	if err := runChatExampleTest(ctx, client, repo); err != nil {
		cancel()
		fmt.Fprintf(os.Stderr, "ERROR: chat example test: %v\n", err)
		return 1
	}
	cancel()

	if err := runChatExampleCLIE2E(target, cacert, cert, key, jwtKey, jwtIssuer, repo); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: chat example CLI e2e: %v\n", err)
		return 1
	}

	ctx, cancel = newStepContext()
	if err := runMultiInputTest(ctx, client, repo); err != nil {
		cancel()
		fmt.Fprintf(os.Stderr, "ERROR: multi-input test: %v\n", err)
		return 1
	}
	cancel()

	ctx, cancel = newStepContext()
	if err := runDisconnectReconnectTest(ctx, client, repo); err != nil {
		cancel()
		fmt.Fprintf(os.Stderr, "ERROR: disconnect/reconnect test: %v\n", err)
		return 1
	}
	cancel()

	sessionID := uuid.NewString()

	fmt.Printf("Starting session %s (repo=%s)...\n", sessionID, repo)

	ctx, cancel = newStepContext()
	defer cancel()

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

// runChatExampleTest exercises the same flow as examples/chat:
// start a session, open an event stream, send a message, receive the echoed
// output, then stop the session. This validates the SDK usage shown in the
// example actually works end-to-end.
func runChatExampleTest(ctx context.Context, client *bridgeclient.Client, repo string) error {
	fmt.Println("Running chat example test...")

	// Step 1: Start a new session (using the echo provider so we get
	// deterministic output without needing a real AI).
	sessionID := uuid.NewString()
	_, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId: "e2e",
		SessionId: sessionID,
		RepoPath:  repo,
		Provider:  "echo",
	})
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	fmt.Printf("  Started session %s\n", sessionID)

	// Step 2: Open an event stream.
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	stream, err := client.StreamEvents(streamCtx, &bridgev1.StreamEventsRequest{
		SessionId:    sessionID,
		SubscriberId: "chat-example-test",
		AfterSeq:     0,
	})
	if err != nil {
		return fmt.Errorf("stream events: %w", err)
	}

	// Step 3: Receive events in the background, collecting stdout output.
	var mu sync.Mutex
	var collected string
	recvDone := make(chan error, 1)
	go func() {
		recvDone <- stream.RecvAll(streamCtx, func(ev *bridgev1.SessionEvent) error {
			if ev.Type == bridgev1.EventType_EVENT_TYPE_STDOUT {
				mu.Lock()
				collected += ev.Text
				mu.Unlock()
			}
			return nil
		})
	}()

	// Step 4: Send a message (like a user typing into the readline prompt).
	message := "hello from the chat example"
	_, err = client.SendInput(ctx, &bridgev1.SendInputRequest{
		SessionId: sessionID,
		Text:      message + "\n",
	})
	if err != nil {
		return fmt.Errorf("send input: %w", err)
	}
	fmt.Printf("  Sent: %q\n", message)

	// Step 5: Wait for the echoed response.
	deadline := time.After(10 * time.Second)
	for {
		mu.Lock()
		got := collected
		mu.Unlock()
		if strings.Contains(got, message) {
			break
		}
		select {
		case <-deadline:
			return fmt.Errorf("timed out waiting for echoed output")
		case <-time.After(100 * time.Millisecond):
		}
	}
	fmt.Println("  Received echoed response")

	// Step 6: Stop the session.
	streamCancel()
	<-recvDone

	_, err = client.StopSession(ctx, &bridgev1.StopSessionRequest{SessionId: sessionID})
	if err != nil {
		return fmt.Errorf("stop session: %w", err)
	}
	fmt.Println("  Session stopped")
	fmt.Println("Chat example test passed.")
	return nil
}

func runChatExampleCLIE2E(target, cacert, cert, key, jwtKey, jwtIssuer, repo string) error {
	fmt.Println("Running chat example CLI e2e test (claude-chat)...")

	apiKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if apiKey == "" {
		return errors.New("ANTHROPIC_API_KEY is required for chat example CLI e2e")
	}

	prompt := "Give a one-line acknowledgment that you received this message."

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/usr/local/bin/chat-example",
		"-target", target,
		"-provider", "claude-chat",
		"-project", "e2e",
		"-cacert", cacert,
		"-cert", cert,
		"-key", key,
		"-jwt-key", jwtKey,
		"-jwt-issuer", jwtIssuer,
		"-timeout", "90s",
		repo,
	)
	cmd.Env = os.Environ()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("start chat-example via pty: %w", err)
	}
	defer func() { _ = ptmx.Close() }()

	var outMu sync.Mutex
	var out bytes.Buffer
	readDone := make(chan error, 1)
	go func() {
		r := bufio.NewReader(ptmx)
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				outMu.Lock()
				out.Write(buf[:n])
				outMu.Unlock()
			}
			if err != nil {
				readDone <- err
				return
			}
		}
	}()

	snapshot := func() string {
		outMu.Lock()
		defer outMu.Unlock()
		return out.String()
	}

	waitContains := func(substr string, timeout time.Duration) error {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if strings.Contains(snapshot(), substr) {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
		return fmt.Errorf("timed out waiting for %q; output:\n%s", substr, snapshot())
	}

	if err := waitContains("you> ", 20*time.Second); err != nil {
		return err
	}

	startOffset := len(snapshot())
	if _, err := io.WriteString(ptmx, prompt+"\n"); err != nil {
		return fmt.Errorf("write prompt: %w", err)
	}

	var assistantChunk string
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		outNow := snapshot()
		if startOffset > len(outNow) {
			startOffset = 0
		}
		window := outNow[startOffset:]
		echoIdx := strings.Index(window, prompt)
		if echoIdx < 0 {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		afterEcho := window[echoIdx+len(prompt):]
		nextPromptIdx := strings.Index(afterEcho, "you> ")
		if nextPromptIdx < 0 {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		between := sanitizeTTYText(afterEcho[:nextPromptIdx])
		assistantChunk = strings.TrimSpace(between)
		if assistantChunk == "" {
			return fmt.Errorf("chat prompt reappeared before assistant output; output:\n%s", outNow)
		}
		break
	}
	if assistantChunk == "" {
		return fmt.Errorf("timed out waiting for assistant output before next prompt; output:\n%s", snapshot())
	}

	if _, err := io.WriteString(ptmx, "/quit\n"); err != nil {
		return fmt.Errorf("write /quit: %w", err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	select {
	case err := <-waitErr:
		if err != nil {
			return fmt.Errorf("chat-example exited with error: %w\noutput:\n%s", err, snapshot())
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		return fmt.Errorf("timed out waiting for chat-example to exit\noutput:\n%s", snapshot())
	}

	select {
	case err := <-readDone:
		if err != nil &&
			!errors.Is(err, io.EOF) &&
			!errors.Is(err, syscall.EIO) &&
			!strings.Contains(strings.ToLower(err.Error()), "input/output error") &&
			!strings.Contains(strings.ToLower(err.Error()), "closed") {
			return fmt.Errorf("pty read error: %w", err)
		}
	default:
	}

	fmt.Println("Chat example CLI e2e test passed.")
	return nil
}

func sanitizeTTYText(s string) string {
	replacer := strings.NewReplacer(
		"\r", "\n",
		"\x1b[K", "",
		"\x1b[0m", "",
		"\x1b[1m", "",
		"\x1b[2m", "",
		"\x1b[22m", "",
		"\x1b[39m", "",
	)
	return replacer.Replace(s)
}

func runMultiInputTest(ctx context.Context, client *bridgeclient.Client, repo string) error {
	fmt.Println("Running multi-input pub/sub test...")

	sessionID := uuid.NewString()
	_, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId: "e2e",
		SessionId: sessionID,
		RepoPath:  repo,
		Provider:  "echo",
	})
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}

	stream, err := client.StreamEvents(ctx, &bridgev1.StreamEventsRequest{
		SessionId:    sessionID,
		SubscriberId: "multi-input-sub",
		AfterSeq:     0,
	})
	if err != nil {
		return fmt.Errorf("stream events: %w", err)
	}

	// Collect STDOUT events in background.
	var mu sync.Mutex
	var outputs []string
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	recvDone := make(chan error, 1)
	go func() {
		recvDone <- stream.RecvAll(streamCtx, func(ev *bridgev1.SessionEvent) error {
			if ev.Type == bridgev1.EventType_EVENT_TYPE_STDOUT {
				mu.Lock()
				outputs = append(outputs, strings.TrimSpace(ev.Text))
				mu.Unlock()
			}
			return nil
		})
	}()

	inputs := []string{"hello-1", "hello-2", "hello-3"}
	for i, msg := range inputs {
		time.Sleep(200 * time.Millisecond)
		_, err := client.SendInput(ctx, &bridgev1.SendInputRequest{
			SessionId: sessionID,
			Text:      msg + "\n",
		})
		if err != nil {
			return fmt.Errorf("send input %d: %w", i+1, err)
		}
		fmt.Printf("  Sent input %d: %q\n", i+1, msg)
	}

	// Wait for all 3 echoed outputs.
	deadline := time.After(10 * time.Second)
	for {
		mu.Lock()
		got := len(outputs)
		mu.Unlock()
		if got >= 3 {
			break
		}
		select {
		case <-deadline:
			mu.Lock()
			defer mu.Unlock()
			return fmt.Errorf("timed out waiting for echoed outputs, got %d: %v", len(outputs), outputs)
		case <-time.After(100 * time.Millisecond):
		}
	}

	streamCancel()
	<-recvDone

	// Stop session.
	_, err = client.StopSession(ctx, &bridgev1.StopSessionRequest{SessionId: sessionID})
	if err != nil {
		return fmt.Errorf("stop session: %w", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for i, expected := range inputs {
		found := false
		for _, o := range outputs {
			if strings.Contains(o, expected) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("expected output %d (%q) not found in %v", i+1, expected, outputs)
		}
	}

	fmt.Println("  OK: received all 3 echoed outputs")
	fmt.Println("Multi-input pub/sub test passed.")
	return nil
}

func runDisconnectReconnectTest(ctx context.Context, client *bridgeclient.Client, repo string) error {
	fmt.Println("Running disconnect/reconnect pub/sub test...")

	sessionID := uuid.NewString()
	subscriberID := "reconnect-sub"

	_, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId: "e2e",
		SessionId: sessionID,
		RepoPath:  repo,
		Provider:  "echo",
	})
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}

	// Phase 1: connect and send first input.
	fmt.Println("  Phase 1: connect and send first input")
	stream1, err := client.StreamEvents(ctx, &bridgev1.StreamEventsRequest{
		SessionId:    sessionID,
		SubscriberId: subscriberID,
		AfterSeq:     0,
	})
	if err != nil {
		return fmt.Errorf("stream events phase 1: %w", err)
	}

	var lastSeq uint64
	phase1Ctx, phase1Cancel := context.WithCancel(ctx)
	defer phase1Cancel()
	recv1Done := make(chan error, 1)
	var phase1Output string
	var phase1Mu sync.Mutex

	go func() {
		recv1Done <- stream1.RecvAll(phase1Ctx, func(ev *bridgev1.SessionEvent) error {
			if ev.Seq > lastSeq {
				lastSeq = ev.Seq
			}
			if ev.Type == bridgev1.EventType_EVENT_TYPE_STDOUT {
				phase1Mu.Lock()
				phase1Output += ev.Text
				phase1Mu.Unlock()
			}
			return nil
		})
	}()

	_, err = client.SendInput(ctx, &bridgev1.SendInputRequest{
		SessionId: sessionID,
		Text:      "before disconnect\n",
	})
	if err != nil {
		return fmt.Errorf("send 'before disconnect': %w", err)
	}

	// Wait for the echo.
	deadline := time.After(10 * time.Second)
	for {
		phase1Mu.Lock()
		got := phase1Output
		phase1Mu.Unlock()
		if strings.Contains(got, "before disconnect") {
			break
		}
		select {
		case <-deadline:
			return fmt.Errorf("timed out waiting for 'before disconnect' echo")
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Phase 2: disconnect.
	fmt.Println("  Phase 2: disconnect")
	phase1Cancel()
	<-recv1Done

	// Phase 3: send input while disconnected.
	fmt.Println("  Phase 3: send input while disconnected")
	time.Sleep(200 * time.Millisecond)
	_, err = client.SendInput(ctx, &bridgev1.SendInputRequest{
		SessionId: sessionID,
		Text:      "during disconnect\n",
	})
	if err != nil {
		return fmt.Errorf("send 'during disconnect': %w", err)
	}

	// Give the echo time to be buffered.
	time.Sleep(500 * time.Millisecond)

	// Phase 4: reconnect with afterSeq.
	fmt.Printf("  Phase 4: reconnect with afterSeq=%d\n", lastSeq)
	stream2, err := client.StreamEvents(ctx, &bridgev1.StreamEventsRequest{
		SessionId:    sessionID,
		SubscriberId: subscriberID,
		AfterSeq:     lastSeq,
	})
	if err != nil {
		return fmt.Errorf("stream events phase 4: %w", err)
	}

	phase4Ctx, phase4Cancel := context.WithCancel(ctx)
	defer phase4Cancel()
	recv2Done := make(chan error, 1)
	var phase4Output string
	var phase4Mu sync.Mutex

	go func() {
		recv2Done <- stream2.RecvAll(phase4Ctx, func(ev *bridgev1.SessionEvent) error {
			if ev.Type == bridgev1.EventType_EVENT_TYPE_STDOUT {
				phase4Mu.Lock()
				phase4Output += ev.Text
				phase4Mu.Unlock()
			}
			return nil
		})
	}()

	// Wait for the replayed "during disconnect" event.
	deadline = time.After(10 * time.Second)
	for {
		phase4Mu.Lock()
		got := phase4Output
		phase4Mu.Unlock()
		if strings.Contains(got, "during disconnect") {
			break
		}
		select {
		case <-deadline:
			return fmt.Errorf("timed out waiting for replayed 'during disconnect' event")
		case <-time.After(100 * time.Millisecond):
		}
	}
	fmt.Println("  OK: received missed event via replay")

	// Send another input to verify live streaming works after reconnect.
	_, err = client.SendInput(ctx, &bridgev1.SendInputRequest{
		SessionId: sessionID,
		Text:      "after reconnect\n",
	})
	if err != nil {
		return fmt.Errorf("send 'after reconnect': %w", err)
	}

	deadline = time.After(10 * time.Second)
	for {
		phase4Mu.Lock()
		got := phase4Output
		phase4Mu.Unlock()
		if strings.Contains(got, "after reconnect") {
			break
		}
		select {
		case <-deadline:
			return fmt.Errorf("timed out waiting for 'after reconnect' echo")
		case <-time.After(100 * time.Millisecond):
		}
	}
	fmt.Println("  OK: received live event after reconnect")

	phase4Cancel()
	<-recv2Done

	// Stop session.
	_, err = client.StopSession(ctx, &bridgev1.StopSessionRequest{SessionId: sessionID})
	if err != nil {
		return fmt.Errorf("stop session: %w", err)
	}

	fmt.Println("Disconnect/reconnect pub/sub test passed.")
	return nil
}

func runMTLSRejectionScenarios(
	ctx context.Context,
	target string,
	timeout time.Duration,
	baseMTLS bridgeclient.MTLSConfig,
	baseJWT bridgeclient.JWTConfig,
) error {
	fmt.Println("Running mTLS rejection scenarios...")

	// Case 1: bad cert (server certificate used as client identity, wrong key usage).
	if err := expectRPCFailure(
		ctx,
		target,
		timeout,
		bridgeclient.MTLSConfig{
			CABundlePath: baseMTLS.CABundlePath,
			CertPath:     "/certs/bridge.crt",
			KeyPath:      "/certs/bridge.key",
			ServerName:   baseMTLS.ServerName,
		},
		baseJWT,
		"mTLS reject: server cert as client cert",
	); err != nil {
		return err
	}

	// Case 2: wrong CA.
	badCAcert, badCAkey, err := writeRogueClientCertPair()
	if err != nil {
		return fmt.Errorf("generate wrong-CA client cert: %w", err)
	}
	if err := expectRPCFailure(
		ctx,
		target,
		timeout,
		bridgeclient.MTLSConfig{
			CABundlePath: baseMTLS.CABundlePath,
			CertPath:     badCAcert,
			KeyPath:      badCAkey,
			ServerName:   baseMTLS.ServerName,
		},
		baseJWT,
		"mTLS reject: client cert signed by wrong CA",
	); err != nil {
		return err
	}

	// Case 3: expired cert.
	expiredCert, expiredKey, err := writeExpiredClientCertPair("/certs/ca.crt", "/certs/ca.key")
	if err != nil {
		return fmt.Errorf("generate expired client cert: %w", err)
	}
	if err := expectRPCFailure(
		ctx,
		target,
		timeout,
		bridgeclient.MTLSConfig{
			CABundlePath: baseMTLS.CABundlePath,
			CertPath:     expiredCert,
			KeyPath:      expiredKey,
			ServerName:   baseMTLS.ServerName,
		},
		baseJWT,
		"mTLS reject: expired client cert",
	); err != nil {
		return err
	}

	fmt.Println("mTLS rejection scenarios passed.")
	return nil
}

func runJWTRejectionScenarios(
	ctx context.Context,
	target string,
	timeout time.Duration,
	baseMTLS bridgeclient.MTLSConfig,
	jwtKey,
	jwtIssuer string,
) error {
	fmt.Println("Running JWT rejection scenarios...")
	tests := []struct {
		name string
		jwt  bridgeclient.JWTConfig
	}{
		{
			name: "JWT reject: wrong issuer",
			jwt: bridgeclient.JWTConfig{
				PrivateKeyPath: jwtKey,
				Issuer:         jwtIssuer + "-wrong",
				Audience:       "bridge",
			},
		},
		{
			name: "JWT reject: wrong audience",
			jwt: bridgeclient.JWTConfig{
				PrivateKeyPath: jwtKey,
				Issuer:         jwtIssuer,
				Audience:       "not-bridge",
			},
		},
		{
			name: "JWT reject: expired token",
			jwt: bridgeclient.JWTConfig{
				PrivateKeyPath: jwtKey,
				Issuer:         jwtIssuer,
				Audience:       "bridge",
				TTL:            -1 * time.Minute,
			},
		},
	}

	for _, tc := range tests {
		if err := expectUnauthorizedFailure(ctx, target, timeout, baseMTLS, tc.jwt, tc.name); err != nil {
			return err
		}
	}

	fmt.Println("JWT rejection scenarios passed.")
	return nil
}

func expectRPCFailure(
	ctx context.Context,
	target string,
	timeout time.Duration,
	mtls bridgeclient.MTLSConfig,
	jwt bridgeclient.JWTConfig,
	name string,
) error {
	client, err := bridgeclient.New(
		bridgeclient.WithTarget(target),
		bridgeclient.WithTimeout(timeout),
		bridgeclient.WithMTLS(mtls),
		bridgeclient.WithJWT(jwt),
	)
	if err != nil {
		return fmt.Errorf("%s: client create failed: %w", name, err)
	}
	defer client.Close()

	client.SetProject("e2e")
	_, err = client.ListProviders(ctx)
	if err == nil {
		return fmt.Errorf("%s: expected RPC failure, got success", name)
	}
	fmt.Printf("  OK: %s\n", name)
	return nil
}

func expectUnauthorizedFailure(
	ctx context.Context,
	target string,
	timeout time.Duration,
	mtls bridgeclient.MTLSConfig,
	jwt bridgeclient.JWTConfig,
	name string,
) error {
	client, err := bridgeclient.New(
		bridgeclient.WithTarget(target),
		bridgeclient.WithTimeout(timeout),
		bridgeclient.WithMTLS(mtls),
		bridgeclient.WithJWT(jwt),
	)
	if err != nil {
		return fmt.Errorf("%s: client create failed: %w", name, err)
	}
	defer client.Close()

	client.SetProject("e2e")
	_, err = client.ListProviders(ctx)
	if !errors.Is(err, bridgeclient.ErrUnauthorized) {
		return fmt.Errorf("%s: expected unauthorized error, got: %v", name, err)
	}
	fmt.Printf("  OK: %s\n", name)
	return nil
}

func writeRogueClientCertPair() (certPath, keyPath string, err error) {
	tmpDir, err := os.MkdirTemp("", "e2e-rogue-ca-*")
	if err != nil {
		return "", "", err
	}

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	now := time.Now()
	caTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(now.UnixNano()),
		Subject:               pkix.Name{CommonName: "rogue-ca"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		return "", "", err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return "", "", err
	}

	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	clientTpl := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano() + 1),
		Subject:      pkix.Name{CommonName: "rogue-client"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTpl, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		return "", "", err
	}

	certPath = filepath.Join(tmpDir, "rogue-client.crt")
	keyPath = filepath.Join(tmpDir, "rogue-client.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}), 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)}), 0o600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func writeExpiredClientCertPair(caCertPath, caKeyPath string) (certPath, keyPath string, err error) {
	caPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return "", "", err
	}
	caBlock, _ := pem.Decode(caPEM)
	if caBlock == nil {
		return "", "", fmt.Errorf("decode CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return "", "", err
	}

	caKeyPEM, err := os.ReadFile(caKeyPath)
	if err != nil {
		return "", "", err
	}
	caKey, err := parsePrivateKeyPEM(caKeyPEM)
	if err != nil {
		return "", "", err
	}

	tmpDir, err := os.MkdirTemp("", "e2e-expired-client-*")
	if err != nil {
		return "", "", err
	}
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}

	now := time.Now()
	clientTpl := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()),
		Subject:      pkix.Name{CommonName: "expired-client"},
		NotBefore:    now.Add(-2 * time.Hour),
		NotAfter:     now.Add(-1 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	clientDER, err := x509.CreateCertificate(rand.Reader, clientTpl, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		return "", "", err
	}

	certPath = filepath.Join(tmpDir, "expired-client.crt")
	keyPath = filepath.Join(tmpDir, "expired-client.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}), 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)}), 0o600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func parsePrivateKeyPEM(p []byte) (any, error) {
	for {
		block, rest := pem.Decode(p)
		if block == nil {
			return nil, fmt.Errorf("decode private key PEM")
		}
		switch block.Type {
		case "RSA PRIVATE KEY":
			return x509.ParsePKCS1PrivateKey(block.Bytes)
		case "EC PRIVATE KEY":
			return x509.ParseECPrivateKey(block.Bytes)
		case "PRIVATE KEY":
			k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, err
			}
			switch key := k.(type) {
			case *rsa.PrivateKey:
				return key, nil
			case *ecdsa.PrivateKey:
				return key, nil
			case ed25519.PrivateKey:
				return key, nil
			default:
				return nil, fmt.Errorf("unsupported PKCS8 key type %T", k)
			}
		}
		p = rest
	}
}
