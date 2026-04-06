//go:build e2e

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

// Flag variables for the test binary.
// Run with: ./e2e-suite -test.v -test.timeout 300s -bridge.target bridge:9445 ...
var (
	suiteTarget  = flag.String("bridge.target", "bridge:9445", "bridge address")
	suiteCACert  = flag.String("bridge.cacert", "", "CA bundle path")
	suiteCert    = flag.String("bridge.cert", "", "client cert path")
	suiteKey     = flag.String("bridge.key", "", "client key path")
	suiteJWTKey  = flag.String("bridge.jwt-key", "", "JWT signing key path")
	suiteIssuer  = flag.String("bridge.jwt-issuer", "e2e", "JWT issuer")
	suiteRepo    = flag.String("bridge.repo", "/tmp/cache-cleaner", "repo path")
	suiteTimeout = flag.Duration("bridge.timeout", 15*time.Minute, "per-scenario timeout")
)

// TestMain ensures custom flags are parsed before running the suite.
func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

// BridgeSuite tests end-to-end provider scenarios against a live bridge daemon.
type BridgeSuite struct {
	suite.Suite
	client *bridgeclient.Client
}

func (s *BridgeSuite) SetupSuite() {
	client, err := bridgeclient.New(
		bridgeclient.WithTarget(*suiteTarget),
		bridgeclient.WithTimeout(*suiteTimeout),
		bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
			CABundlePath: *suiteCACert,
			CertPath:     *suiteCert,
			KeyPath:      *suiteKey,
			ServerName:   "bridge",
		}),
		bridgeclient.WithJWT(bridgeclient.JWTConfig{
			PrivateKeyPath: *suiteJWTKey,
			Issuer:         *suiteIssuer,
			Audience:       "bridge",
		}),
	)
	s.Require().NoError(err, "connect to bridge")
	client.SetProject("e2e")
	s.client = client
}

func (s *BridgeSuite) TearDownSuite() {
	if s.client != nil {
		_ = s.client.Close()
	}
}

func (s *BridgeSuite) TestClaude() {
	s.runProviderScenario(scenarios[0])
}

func (s *BridgeSuite) TestOpencode() {
	s.runProviderScenario(scenarios[1])
}

func (s *BridgeSuite) TestGemini() {
	s.runProviderScenario(scenarios[2])
}

func (s *BridgeSuite) runProviderScenario(scenario providerScenario) {
	if os.Getenv(scenario.requiredEnv) == "" {
		s.T().Skipf("skipping %s: %s not set", scenario.name, scenario.requiredEnv)
	}
	err := s.executeScenario(scenario)
	s.Require().NoError(err, "provider scenario %s", scenario.name)
}

// executeScenario runs a multi-turn provider conversation and asserts correctness.
func (s *BridgeSuite) executeScenario(scenario providerScenario) error {
	ctx, cancel := context.WithTimeout(context.Background(), *suiteTimeout)
	defer cancel()

	sessionID := uuid.NewString()
	_, err := s.client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   "e2e",
		SessionId:   sessionID,
		RepoPath:    *suiteRepo,
		Provider:    scenario.name,
		InitialCols: 120,
		InitialRows: 40,
	})
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}

	stream, err := s.client.AttachSession(ctx, &bridgev1.AttachSessionRequest{
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
	if _, err := s.client.WriteInput(ctx, &bridgev1.WriteInputRequest{
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

	if _, err := s.client.WriteInput(ctx, &bridgev1.WriteInputRequest{
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
	if _, err := s.client.WriteInput(ctx, &bridgev1.WriteInputRequest{
		SessionId: sessionID,
		ClientId:  stream.ClientID(),
		Data:      []byte("Blue. Reply with exactly " + turn3Marker + " and nothing else.\n"),
	}); err != nil {
		return fmt.Errorf("write turn 3: %w", err)
	}
	if err := waitForLiteral(&log, turn3Marker, scenario.turnTimeout); err != nil {
		return fmt.Errorf("turn 3 response: %w\ntranscript:\n%s", err, log.snapshot())
	}

	_, _ = s.client.StopSession(context.Background(), &bridgev1.StopSessionRequest{
		SessionId: sessionID,
		Force:     true,
	})
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

// TestBridgeSuite is the entry point that runs all provider tests.
func TestBridgeSuite(t *testing.T) {
	suite.Run(t, new(BridgeSuite))
}
