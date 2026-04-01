package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

type providerScenario struct {
	name          string
	requiredEnv   string
	promptRe      *regexp.Regexp
	startTimeout  time.Duration
	turnTimeout   time.Duration
	questionCheck *regexp.Regexp
}

var scenarios = []providerScenario{
	{
		name:          "claude",
		requiredEnv:   "ANTHROPIC_API_KEY",
		promptRe:      regexp.MustCompile(`(?m)(❯|>\s*$)`),
		startTimeout:  90 * time.Second,
		turnTimeout:   180 * time.Second,
		questionCheck: regexp.MustCompile(`\?`),
	},
	{
		name:          "opencode",
		requiredEnv:   "OPENAI_API_KEY",
		promptRe:      regexp.MustCompile(`❯`),
		startTimeout:  90 * time.Second,
		turnTimeout:   240 * time.Second,
		questionCheck: regexp.MustCompile(`\?`),
	},
	{
		name:          "gemini",
		requiredEnv:   "GEMINI_API_KEY",
		promptRe:      regexp.MustCompile(`(?m)>\s*$`),
		startTimeout:  120 * time.Second,
		turnTimeout:   240 * time.Second,
		questionCheck: regexp.MustCompile(`\?`),
	},
}

type transcript struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (t *transcript) append(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.Write(p)
}

func (t *transcript) snapshot() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}

func main() {
	target := flag.String("target", "bridge:9445", "bridge address")
	cacert := flag.String("cacert", "", "CA bundle path")
	cert := flag.String("cert", "", "client cert path")
	key := flag.String("key", "", "client key path")
	jwtKey := flag.String("jwt-key", "", "JWT signing key path")
	jwtIssuer := flag.String("jwt-issuer", "e2e", "JWT issuer")
	repo := flag.String("repo", "/tmp/cache-cleaner", "repo path")
	timeout := flag.Duration("timeout", 15*time.Minute, "overall timeout")
	only := flag.String("only", "all", "test subset: all, claude, opencode, gemini")
	flag.Parse()

	client, err := bridgeclient.New(
		bridgeclient.WithTarget(*target),
		bridgeclient.WithTimeout(*timeout),
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: *cacert,
			CertPath:     *cert,
			KeyPath:      *key,
			ServerName:   "bridge",
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
	defer func() {
		_ = client.Close()
	}()
	client.SetProject("e2e")

	var failures []string
	for _, scenario := range scenarios {
		if *only != "all" && *only != scenario.name {
			continue
		}
		if strings.TrimSpace(os.Getenv(scenario.requiredEnv)) == "" {
			fmt.Printf("SKIP %s: missing %s\n", scenario.name, scenario.requiredEnv)
			continue
		}
		if err := runScenario(*timeout, client, *repo, scenario); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", scenario.name, err))
			fmt.Printf("FAIL %s: %v\n", scenario.name, err)
			continue
		}
		fmt.Printf("PASS %s\n", scenario.name)
	}
	if len(failures) > 0 {
		for _, item := range failures {
			fmt.Fprintln(os.Stderr, item)
		}
		os.Exit(1)
	}
}

func runScenario(timeout time.Duration, client *bridgeclient.Client, repo string, scenario providerScenario) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	sessionID := uuid.NewString()
	if _, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   "e2e",
		SessionId:   sessionID,
		RepoPath:    repo,
		Provider:    scenario.name,
		InitialCols: 120,
		InitialRows: 40,
	}); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	stream, err := client.AttachSession(ctx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  uuid.NewString(),
	})
	if err != nil {
		return fmt.Errorf("attach: %w", err)
	}

	var log transcript
	done := make(chan error, 1)
	go func() {
		done <- stream.RecvAll(ctx, func(ev *bridgev1.AttachSessionEvent) error {
			if ev.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_OUTPUT {
				log.append(ev.Payload)
			}
			if ev.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ERROR {
				return errors.New(ev.Error)
			}
			return nil
		})
	}()

	if err := waitForMatch(&log, scenario.promptRe, scenario.startTimeout); err != nil {
		return fmt.Errorf("initial prompt: %w\ntranscript:\n%s", err, log.snapshot())
	}

	turn1Marker := "BRIDGE_TURN_ONE_OK"
	if _, err := client.WriteInput(ctx, &bridgev1.WriteInputRequest{
		SessionId: sessionID,
		ClientId:  stream.ClientID(),
		Data:      []byte("Reply with exactly " + turn1Marker + " and nothing else.\n"),
	}); err != nil {
		return fmt.Errorf("write turn 1: %w", err)
	}
	if err := waitForLiteral(&log, turn1Marker, scenario.turnTimeout); err != nil {
		return fmt.Errorf("turn 1 response: %w\ntranscript:\n%s", err, log.snapshot())
	}
	if err := waitForMatch(&log, scenario.promptRe, scenario.turnTimeout); err != nil {
		return fmt.Errorf("turn 1 prompt return: %w\ntranscript:\n%s", err, log.snapshot())
	}

	if _, err := client.WriteInput(ctx, &bridgev1.WriteInputRequest{
		SessionId: sessionID,
		ClientId:  stream.ClientID(),
		Data:      []byte("Ask me exactly one short clarifying question, then wait for my answer.\n"),
	}); err != nil {
		return fmt.Errorf("write turn 2: %w", err)
	}
	if err := waitForMatch(&log, scenario.questionCheck, scenario.turnTimeout); err != nil {
		return fmt.Errorf("turn 2 question: %w\ntranscript:\n%s", err, log.snapshot())
	}
	if err := waitForMatch(&log, scenario.promptRe, scenario.turnTimeout); err != nil {
		return fmt.Errorf("turn 2 prompt return: %w\ntranscript:\n%s", err, log.snapshot())
	}

	turn3Marker := "BRIDGE_FOLLOWUP_OK"
	if _, err := client.WriteInput(ctx, &bridgev1.WriteInputRequest{
		SessionId: sessionID,
		ClientId:  stream.ClientID(),
		Data:      []byte("Blue. Reply with exactly " + turn3Marker + " and nothing else.\n"),
	}); err != nil {
		return fmt.Errorf("write turn 3: %w", err)
	}
	if err := waitForLiteral(&log, turn3Marker, scenario.turnTimeout); err != nil {
		return fmt.Errorf("turn 3 response: %w\ntranscript:\n%s", err, log.snapshot())
	}

	_, _ = client.StopSession(context.Background(), &bridgev1.StopSessionRequest{SessionId: sessionID, Force: true})
	cancel()
	select {
	case err := <-done:
		if err != nil && ctx.Err() == nil {
			return fmt.Errorf("stream: %w", err)
		}
	case <-time.After(5 * time.Second):
	}
	return nil
}

func waitForLiteral(log *transcript, needle string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(log.snapshot(), needle) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %q", needle)
}

func waitForMatch(log *transcript, re *regexp.Regexp, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if re.MatchString(log.snapshot()) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", re.String())
}
