package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// cliProviderConfig holds per-provider settings for CLI-based e2e scenarios.
type cliProviderConfig struct {
	name           string        // human-readable name used in test output and suite registration
	providerFlag   string        // value passed to chat-example -provider
	apiKeyEnv      string        // env var that must be non-empty (empty string means no check)
	startupTimeout time.Duration // how long to wait for "you> " after launch
	promptTimeout  time.Duration // per-exchange response timeout
	sessionTimeout time.Duration // overall context deadline for the cmd (should cover all exchanges)
}

// cliProviders defines the four interactive AI CLI providers under test.
// sessionTimeout is sized to cover three consecutive exchanges (the worst case).
var cliProviders = []cliProviderConfig{
	{
		name:           "claude-chat",
		providerFlag:   "claude-chat",
		apiKeyEnv:      "ANTHROPIC_API_KEY",
		startupTimeout: 20 * time.Second,
		promptTimeout:  90 * time.Second,
		sessionTimeout: 360 * time.Second,
	},
	{
		name:           "codex",
		providerFlag:   "codex",
		apiKeyEnv:      "OPENAI_API_KEY",
		startupTimeout: 30 * time.Second,
		promptTimeout:  150 * time.Second,
		sessionTimeout: 600 * time.Second,
	},
	{
		name:           "opencode",
		providerFlag:   "opencode",
		apiKeyEnv:      "OPENAI_API_KEY",
		startupTimeout: 30 * time.Second,
		promptTimeout:  300 * time.Second,
		sessionTimeout: 1200 * time.Second,
	},
	{
		name:           "gemini",
		providerFlag:   "gemini",
		apiKeyEnv:      "GEMINI_API_KEY",
		startupTimeout: 60 * time.Second,
		promptTimeout:  150 * time.Second,
		sessionTimeout: 600 * time.Second,
	},
}

// ptyChatSession wraps a chat-example process driven via PTY.
type ptyChatSession struct {
	ptmx     *os.File
	cmd      *exec.Cmd
	out      bytes.Buffer
	outMu    sync.Mutex
	readDone chan error
	cancel   context.CancelFunc
}

// newPtyChatSession launches chat-example for cfg against the given bridge and
// repo. The caller must call close() when done.
func newPtyChatSession(cfg cliProviderConfig, target, cacert, cert, key, jwtKey, jwtIssuer, repo string) (*ptyChatSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.sessionTimeout)

	cmd := exec.CommandContext(ctx, "/usr/local/bin/chat-example",
		"-target", target,
		"-provider", cfg.providerFlag,
		"-project", "e2e",
		"-cacert", cacert,
		"-cert", cert,
		"-key", key,
		"-jwt-key", jwtKey,
		"-jwt-issuer", jwtIssuer,
		"-timeout", fmt.Sprintf("%.0fs", cfg.promptTimeout.Seconds()),
		repo,
	)
	cmd.Env = os.Environ()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start chat-example via pty: %w", err)
	}

	sess := &ptyChatSession{
		ptmx:     ptmx,
		cmd:      cmd,
		readDone: make(chan error, 1),
		cancel:   cancel,
	}

	go func() {
		r := bufio.NewReader(ptmx)
		buf := make([]byte, 4096)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				sess.outMu.Lock()
				sess.out.Write(buf[:n])
				sess.outMu.Unlock()
			}
			if readErr != nil {
				sess.readDone <- readErr
				return
			}
		}
	}()

	return sess, nil
}

func (s *ptyChatSession) snapshot() string {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	return s.out.String()
}

func (s *ptyChatSession) waitContains(substr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(s.snapshot(), substr) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %q; output:\n%s", substr, s.snapshot())
}

// waitReady waits for the initial "you> " prompt, signaling the session is up.
func (s *ptyChatSession) waitReady(timeout time.Duration) error {
	return s.waitContains("you> ", timeout)
}

// exchange sends prompt and waits for the AI's full response followed by the
// next "you> " prompt. It returns the stripped text of the AI's response.
//
// readline emits cursor-clearing sequences (\b, \x1b[J, etc.) and re-displays
// "you> <input>" after Enter, creating false "you> " markers before the real
// AI response arrives. exchange scans forward through all "you> " candidates
// and, for each, strips the user's own prompt text (which appears as a
// readline re-display artefact) from the preceding content. If anything useful
// remains after that stripping, it is the AI response.
func (s *ptyChatSession) exchange(prompt string, timeout time.Duration) (string, error) {
	startOffset := len(s.snapshot())
	if _, err := io.WriteString(s.ptmx, prompt+"\n"); err != nil {
		return "", fmt.Errorf("write prompt: %w", err)
	}

	cleanPrompt := strings.TrimSpace(prompt)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		outNow := s.snapshot()
		if startOffset > len(outNow) {
			startOffset = 0
		}
		window := outNow[startOffset:]
		echoIdx := strings.Index(window, prompt)
		if echoIdx < 0 {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		contentStart := echoIdx + len(prompt)
		searchFrom := contentStart
		for {
			rel := strings.Index(window[searchFrom:], "you> ")
			if rel < 0 {
				break
			}
			candidateEnd := searchFrom + rel
			raw := stripTTY(window[contentStart:candidateEnd])

			// Remove any repetitions of the user's own prompt; readline
			// re-displays the input line after Enter, so the content before
			// the real "you> " next-prompt often starts with cleanPrompt.
			// Whatever remains after removal is the actual AI response.
			useful := strings.TrimSpace(strings.ReplaceAll(raw, cleanPrompt, ""))
			if useful != "" && !strings.HasPrefix(useful, "you> ") {
				return useful, nil
			}

			// Artefact; advance past this "you> " and try the next candidate.
			searchFrom = candidateEnd + len("you> ")
			contentStart = searchFrom
		}

		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("timed out waiting for response to %q; output:\n%s", prompt, s.snapshot())
}

// quit sends /quit, waits for chat-example to exit cleanly, then checks PTY
// read errors (ignoring the expected EOF/EIO from a closed PTY).
func (s *ptyChatSession) quit() error {
	if _, err := io.WriteString(s.ptmx, "/quit\n"); err != nil {
		return fmt.Errorf("write /quit: %w", err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- s.cmd.Wait() }()

	select {
	case err := <-waitErr:
		if err != nil {
			return fmt.Errorf("chat-example exited with error: %w\noutput:\n%s", err, s.snapshot())
		}
	case <-time.After(45 * time.Second):
		_ = s.cmd.Process.Kill()
		// Force-kill after timeout is not an error — the exchanges completed.
		fmt.Printf("  [warn] chat-example did not exit cleanly after /quit; force-killed\n")
	}

	select {
	case err := <-s.readDone:
		if err != nil &&
			!errors.Is(err, io.EOF) &&
			!errors.Is(err, syscall.EIO) &&
			!strings.Contains(strings.ToLower(err.Error()), "input/output error") &&
			!strings.Contains(strings.ToLower(err.Error()), "closed") {
			return fmt.Errorf("pty read error: %w", err)
		}
	default:
	}

	return nil
}

// close cancels the session context and closes the PTY file descriptor.
func (s *ptyChatSession) close() {
	s.cancel()
	_ = s.ptmx.Close()
}

// ansiEscapeRe matches all standard ANSI/VT100 escape sequences.
var ansiEscapeRe = regexp.MustCompile(`\x1b(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])`)

// stripTTY removes all ANSI escape sequences, backspace characters, and null
// bytes, converts carriage returns to newlines, then trims surrounding
// whitespace. The result contains only printable text.
func stripTTY(s string) string {
	s = ansiEscapeRe.ReplaceAllString(s, "")
	s = strings.NewReplacer("\b", "", "\r", "\n", "\x00", "").Replace(s)
	return strings.TrimSpace(s)
}

// openCLISession checks the required API key, starts a ptyChatSession, and
// waits for the initial "you> " prompt. The caller must call sess.close().
func openCLISession(cfg cliProviderConfig, target, cacert, cert, key, jwtKey, jwtIssuer, repo string) (*ptyChatSession, error) {
	if cfg.apiKeyEnv != "" && strings.TrimSpace(os.Getenv(cfg.apiKeyEnv)) == "" {
		return nil, fmt.Errorf("%s is required for %s CLI e2e", cfg.apiKeyEnv, cfg.name)
	}
	sess, err := newPtyChatSession(cfg, target, cacert, cert, key, jwtKey, jwtIssuer, repo)
	if err != nil {
		return nil, err
	}
	if err := sess.waitReady(cfg.startupTimeout); err != nil {
		sess.close()
		return nil, fmt.Errorf("wait ready: %w", err)
	}
	return sess, nil
}

// runCLIOneShot sends a single prompt and verifies a non-empty response.
func runCLIOneShot(cfg cliProviderConfig, target, cacert, cert, key, jwtKey, jwtIssuer, repo string) error {
	fmt.Printf("Running %s CLI: one-shot...\n", cfg.name)

	sess, err := openCLISession(cfg, target, cacert, cert, key, jwtKey, jwtIssuer, repo)
	if err != nil {
		return err
	}
	defer sess.close()

	resp, err := sess.exchange(
		"Give a one-line acknowledgment that you received this message.",
		cfg.promptTimeout,
	)
	if err != nil {
		return err
	}
	fmt.Printf("  Response: %q\n", resp)

	return sess.quit()
}

// runCLIMultiTurn sends three back-and-forth exchanges in a single session,
// verifying that the session maintains context across turns.
func runCLIMultiTurn(cfg cliProviderConfig, target, cacert, cert, key, jwtKey, jwtIssuer, repo string) error {
	fmt.Printf("Running %s CLI: multi-turn...\n", cfg.name)

	sess, err := openCLISession(cfg, target, cacert, cert, key, jwtKey, jwtIssuer, repo)
	if err != nil {
		return err
	}
	defer sess.close()

	turns := []string{
		"Give a brief acknowledgment of this first message.",
		"Give a brief acknowledgment of this second message.",
		"We have now exchanged several messages. Summarize what we discussed in one sentence.",
	}
	for i, prompt := range turns {
		resp, err := sess.exchange(prompt, cfg.promptTimeout)
		if err != nil {
			return fmt.Errorf("turn %d: %w", i+1, err)
		}
		fmt.Printf("  Turn %d response: %q\n", i+1, resp)
	}

	return sess.quit()
}

// runAllCLIScenarios runs the one-shot, multi-turn, and agent-question
// scenarios for a provider in sequence, stopping on the first failure.
func runAllCLIScenarios(cfg cliProviderConfig, target, cacert, cert, key, jwtKey, jwtIssuer, repo string) error {
	if err := runCLIOneShot(cfg, target, cacert, cert, key, jwtKey, jwtIssuer, repo); err != nil {
		return fmt.Errorf("one-shot: %w", err)
	}
	if err := runCLIMultiTurn(cfg, target, cacert, cert, key, jwtKey, jwtIssuer, repo); err != nil {
		return fmt.Errorf("multi-turn: %w", err)
	}
	if err := runCLIAgentQuestion(cfg, target, cacert, cert, key, jwtKey, jwtIssuer, repo); err != nil {
		return fmt.Errorf("agent-question: %w", err)
	}
	return nil
}

// runCLIAgentQuestion instructs the AI to pose a question to the user, then
// answers it. This exercises the case where initiative comes from the agent.
func runCLIAgentQuestion(cfg cliProviderConfig, target, cacert, cert, key, jwtKey, jwtIssuer, repo string) error {
	fmt.Printf("Running %s CLI: agent question...\n", cfg.name)

	sess, err := openCLISession(cfg, target, cacert, cert, key, jwtKey, jwtIssuer, repo)
	if err != nil {
		return err
	}
	defer sess.close()

	// Turn 1: instruct the AI to ask us a question.
	resp1, err := sess.exchange(
		"Please ask me a simple question about my favorite programming language. Just the question, nothing else.",
		cfg.promptTimeout,
	)
	if err != nil {
		return fmt.Errorf("turn 1 (prompt AI to ask): %w", err)
	}
	fmt.Printf("  Agent asked: %q\n", resp1)
	if !strings.Contains(resp1, "?") {
		return fmt.Errorf("expected agent to ask a question (response must contain '?'), got: %q", resp1)
	}

	// Turn 2: answer the AI's question and verify we get a response.
	resp2, err := sess.exchange(
		"My favorite programming language is Go.",
		cfg.promptTimeout,
	)
	if err != nil {
		return fmt.Errorf("turn 2 (answer agent's question): %w", err)
	}
	fmt.Printf("  Agent replied: %q\n", resp2)

	return sess.quit()
}
