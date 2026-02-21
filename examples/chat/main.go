// Command chat is an interactive prompt loop that keeps a single bridge
// session alive and sends each input to that same session.
//
// Usage:
//
//	go run ./examples/chat [flags] <repo-path>
//
// Example:
//
//	go run ./examples/chat -target 127.0.0.1:9445 \
//	  -provider claude-chat \
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
	"sync"
	"time"

	"github.com/chzyer/readline"
	"github.com/google/uuid"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

var errWaitCancelled = errors.New("wait cancelled")

type responseTracker struct {
	mu            sync.Mutex
	lastOutputSeq uint64
	waiter        *promptWaiter
}

type promptWaiter struct {
	minSeq    uint64
	done      chan error
	timer     *time.Timer
	sawOutput bool
}

func newResponseTracker() *responseTracker {
	return &responseTracker{}
}

func (t *responseTracker) begin(minSeq uint64, timeout, idle time.Duration) (<-chan error, func()) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.waiter != nil {
		t.waiter.timer.Stop()
		t.waiter.done <- errors.New("internal error: previous prompt still pending")
	}

	w := &promptWaiter{
		minSeq: minSeq,
		done:   make(chan error, 1),
	}
	initialWait := timeout
	if t.lastOutputSeq > minSeq {
		w.sawOutput = true
		initialWait = idle
	}
	w.timer = time.AfterFunc(initialWait, func() {
		t.mu.Lock()
		current := t.waiter == w
		sawOutput := w.sawOutput
		t.mu.Unlock()
		if !current {
			return
		}
		if sawOutput {
			t.complete(w, nil)
			return
		}
		t.complete(w, fmt.Errorf("timed out waiting for response after %s", timeout))
	})
	t.waiter = w

	// Keep idle in signature for caller clarity; callback behavior above relies on
	// sawOutput toggled by onOutput after first streamed token.
	_ = idle
	return w.done, func() {
		t.complete(w, errWaitCancelled)
	}
}

func (t *responseTracker) onOutput(ev *bridgev1.SessionEvent, idle time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if ev.Seq > t.lastOutputSeq {
		t.lastOutputSeq = ev.Seq
	}
	w := t.waiter
	if w == nil || ev.Seq <= w.minSeq {
		return
	}
	w.sawOutput = true
	if !w.timer.Stop() {
		// Timer may already be firing; callback guards on pointer equality in complete().
	}
	w.timer.Reset(idle)
}

// onResponseComplete immediately completes the active waiter when the agent
// signals it has finished responding. This replaces the idle-timer path when
// the provider emits EVENT_TYPE_RESPONSE_COMPLETE.
func (t *responseTracker) onResponseComplete() {
	t.mu.Lock()
	w := t.waiter
	t.mu.Unlock()
	if w == nil {
		return
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	t.complete(w, nil)
}

func (t *responseTracker) onTerminal(ev *bridgev1.SessionEvent) {
	switch ev.Type {
	case bridgev1.EventType_EVENT_TYPE_SESSION_FAILED:
		t.completeCurrent(fmt.Errorf("session failed: %s", ev.Error))
	case bridgev1.EventType_EVENT_TYPE_SESSION_STOPPED:
		t.completeCurrent(errors.New("session stopped"))
	}
}

func (t *responseTracker) completeCurrent(err error) {
	t.mu.Lock()
	w := t.waiter
	t.mu.Unlock()
	if w == nil {
		return
	}
	t.complete(w, err)
}

func (t *responseTracker) complete(w *promptWaiter, err error) {
	t.mu.Lock()
	if t.waiter != w {
		t.mu.Unlock()
		return
	}
	t.waiter = nil
	t.mu.Unlock()
	w.done <- err
}

func main() {
	target := flag.String("target", "127.0.0.1:9445", "bridge gRPC address")
	project := flag.String("project", "dev", "project ID")
	provider := flag.String("provider", "claude-chat", "provider name (must support interactive stdin, e.g. codex, opencode, claude-chat)")
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
	}()

	sessionID := uuid.NewString()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	_, err = client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId: *project,
		SessionId: sessionID,
		RepoPath:  repoPath,
		Provider:  *provider,
	})
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start session: %v\n", err)
		os.Exit(1)
	}

	streamCtx, streamCancel := context.WithCancel(context.Background())
	var streamWG sync.WaitGroup
	sessionDone := make(chan struct{})
	tracker := newResponseTracker()
	const responseIdle = 800 * time.Millisecond
	streamWG.Add(1)
	go func() {
		defer streamWG.Done()
		stream, err := client.StreamEvents(streamCtx, &bridgev1.StreamEventsRequest{
			SessionId: sessionID,
			AfterSeq:  0,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open event stream: %v\n", err)
			close(sessionDone)
			return
		}
		err = stream.RecvAll(streamCtx, func(ev *bridgev1.SessionEvent) error {
			switch ev.Type {
			case bridgev1.EventType_EVENT_TYPE_STDOUT:
				fmt.Print(ev.Text)
				tracker.onOutput(ev, responseIdle)
			case bridgev1.EventType_EVENT_TYPE_STDERR:
				fmt.Fprint(os.Stderr, ev.Text)
			case bridgev1.EventType_EVENT_TYPE_RESPONSE_COMPLETE:
				// Agent explicitly signaled it finished — complete immediately
				// rather than waiting for the idle timer.
				tracker.onResponseComplete()
			case bridgev1.EventType_EVENT_TYPE_AGENT_READY:
				// Nothing to do in the loop; the readline prompt is already shown.
			case bridgev1.EventType_EVENT_TYPE_SESSION_FAILED:
				fmt.Fprintf(os.Stderr, "\nSession FAILED: %s\n", ev.Error)
				tracker.onTerminal(ev)
				select {
				case <-sessionDone:
				default:
					close(sessionDone)
				}
			case bridgev1.EventType_EVENT_TYPE_SESSION_STOPPED:
				tracker.onTerminal(ev)
				select {
				case <-sessionDone:
				default:
					close(sessionDone)
				}
			}
			return nil
		})
		if err != nil && streamCtx.Err() == nil {
			fmt.Fprintf(os.Stderr, "stream error: %v\n", err)
			select {
			case <-sessionDone:
			default:
				close(sessionDone)
			}
		}
	}()

	fmt.Fprintln(os.Stderr, "Type a prompt and press Enter. Type /quit to exit.")
	fmt.Fprintf(os.Stderr, "Using session: %s\n", sessionID)
	fmt.Fprintln(os.Stderr, "---")

	for {
		select {
		case <-sessionDone:
			goto shutdown
		default:
		}

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

		select {
		case <-sessionDone:
			fmt.Fprintln(os.Stderr, "session ended; exiting chat")
			goto shutdown
		default:
		}

		if err := sendPrompt(client, sessionID, prompt, *timeout, tracker, responseIdle); err != nil {
			fmt.Fprintf(os.Stderr, "failed to send input: %v\n", err)
		}
	}

shutdown:
	streamCancel()
	streamWG.Wait()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, _ = client.StopSession(stopCtx, &bridgev1.StopSessionRequest{SessionId: sessionID})
	stopCancel()

	fmt.Fprintln(os.Stderr, "Goodbye!")
}

func sendPrompt(client *bridgeclient.Client, sessionID, prompt string, timeout time.Duration, tracker *responseTracker, idle time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	resp, err := client.SendInput(ctx, &bridgev1.SendInputRequest{
		SessionId: sessionID,
		Text:      prompt,
	})
	if err != nil {
		return err
	}
	done, cancelWait := tracker.begin(resp.Seq, timeout, idle)
	defer cancelWait()
	select {
	case err := <-done:
		if err != nil {
			if errors.Is(err, errWaitCancelled) {
				return nil
			}
			return err
		}
		fmt.Println()
		return nil
	case <-ctx.Done():
		return fmt.Errorf("timed out waiting for response after %s", timeout)
	}
}
