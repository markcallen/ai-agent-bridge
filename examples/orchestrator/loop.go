package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	bridgev1 "github.com/orchael/bridgectl/gen/bridge/v1"
)

// run is the main orchestration loop:
//
//  1. Start the session and send the initial task prompt.
//  2. Detach to watch-only mode and buffer output.
//  3. Periodically analyze the buffer with the LLM.
//  4. If done: stop. If stuck: re-attach and send corrective input. If working: keep watching.
func (o *orchestrator) run(ctx context.Context) error {
	// ── 1. Start ─────────────────────────────────────────────────────────────

	sessionID := uuid.NewString()
	fmt.Fprintf(os.Stderr, "[orchestrator] starting session %s\n", sessionID)
	fmt.Fprintf(os.Stderr, "[orchestrator] machine=%s  repo=%s  provider=%s\n",
		o.machine, o.repoPath, o.provider)
	fmt.Fprintf(os.Stderr, "[orchestrator] task: %s\n\n", o.task)

	if _, err := o.client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId:   o.project,
		SessionId:   sessionID,
		RepoPath:    o.repoPath,
		Provider:    o.provider,
		InitialCols: 80,
		InitialRows: 24,
	}); err != nil {
		return fmt.Errorf("start session: %w", err)
	}

	// Give the agent a moment to boot before sending the prompt.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}

	// ── 2. Send initial task prompt ───────────────────────────────────────────

	clientID := uuid.NewString()
	if err := o.sendInput(ctx, sessionID, clientID, o.task+"\r"); err != nil {
		o.stopSession(sessionID)
		return fmt.Errorf("send task: %w", err)
	}
	fmt.Fprintln(os.Stderr, "[orchestrator] task prompt sent — switching to watch mode")

	// ── 3. Watch → Analyze → Act loop ────────────────────────────────────────

	buf := newOutputBuffer(64 * 1024) // keep last 64 KB of output
	attempt := 0

	for {
		select {
		case <-ctx.Done():
			o.stopSession(sessionID)
			return ctx.Err()
		default:
		}

		attempt++
		fmt.Fprintf(os.Stderr, "\n[orchestrator] watch cycle %d — collecting output for %v\n",
			attempt, o.interval)

		// Watch: collect output for one interval, then stop and analyze.
		seqBefore := buf.idleSeq()
		streamErr := o.watchFor(ctx, sessionID, o.interval, buf)
		agentIdle := buf.idleSeq() == seqBefore
		if streamErr != nil && !errors.Is(streamErr, errWatchTimeout) && !errors.Is(streamErr, context.Canceled) {
			fmt.Fprintf(os.Stderr, "[orchestrator] watch error: %v\n", streamErr)
		}

		if errors.Is(streamErr, context.Canceled) {
			o.stopSession(sessionID)
			return nil
		}

		// ── 4. Analyze ───────────────────────────────────────────────────────

		snapshot := buf.snapshot()
		if agentIdle {
			fmt.Fprintln(os.Stderr, "[orchestrator] no new output this interval — agent may be idle")
		}
		fmt.Fprintln(os.Stderr, "[orchestrator] analyzing output with LLM...")

		decision, corrective, err := o.analyzer.analyze(ctx, o.task, snapshot, agentIdle)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[orchestrator] analysis error: %v — will keep watching\n", err)
			decision = decisionWorking
		}

		fmt.Fprintf(os.Stderr, "[orchestrator] decision: %s\n", decision)

		switch decision {

		// ── Done ─────────────────────────────────────────────────────────────
		case decisionDone:
			fmt.Fprintln(os.Stderr, "[orchestrator] agent reached completion — stopping session")
			o.stopSession(sessionID)
			return nil

		// ── Stuck: re-attach and send corrective input ────────────────────────
		case decisionStuck:
			fmt.Fprintf(os.Stderr, "[orchestrator] agent stuck — re-attaching to send: %q\n", corrective)
			clientID = uuid.NewString()
			if err := o.sendInput(ctx, sessionID, clientID, corrective+"\r"); err != nil {
				fmt.Fprintf(os.Stderr, "[orchestrator] send failed: %v\n", err)
			}
			// Reset buffer after intervening so stale context doesn't confuse
			// the next analysis.
			buf.reset()

		// ── Still working: keep watching ──────────────────────────────────────
		case decisionWorking:
			fmt.Fprintln(os.Stderr, "[orchestrator] agent still working — continuing to watch")
		}
	}
}

// watchFor attaches as an observer and drains output into buf for at most dur.
// Returns errWatchTimeout when the interval expires normally.
func (o *orchestrator) watchFor(ctx context.Context, sessionID string, dur time.Duration, buf *outputBuffer) error {
	watchCtx, cancel := context.WithTimeout(ctx, dur)
	defer cancel()

	stream, err := o.client.AttachSession(watchCtx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  uuid.NewString(),
		AfterSeq:  buf.lastSeq(),
		Role:      bridgev1.AttachRole_ATTACH_ROLE_OBSERVER,
	})
	if err != nil {
		return fmt.Errorf("attach observer: %w", err)
	}

	err = stream.RecvAll(watchCtx, func(ev *bridgev1.AttachSessionEvent) error {
		switch ev.Type {
		case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_OUTPUT:
			buf.write(ev.Seq, ev.Payload)
		case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ERROR:
			return errors.New(ev.Error)
		}
		return nil
	})

	if err == nil || errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		return errWatchTimeout
	}
	return err
}

// sendInput attaches as a writer, waits for server confirmation, sends data, then detaches.
func (o *orchestrator) sendInput(ctx context.Context, sessionID, clientID, data string) error {
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	stream, err := o.client.AttachSession(writeCtx, &bridgev1.AttachSessionRequest{
		SessionId: sessionID,
		ClientId:  clientID,
		Role:      bridgev1.AttachRole_ATTACH_ROLE_WRITER,
	})
	if err != nil {
		return fmt.Errorf("attach writer: %w", err)
	}

	// attached is closed when the server sends ATTACH_EVENT_TYPE_ATTACHED,
	// confirming this client is registered as the writer.
	attached := make(chan struct{})
	recvDone := make(chan error, 1)
	go func() {
		recvDone <- stream.RecvAll(writeCtx, func(ev *bridgev1.AttachSessionEvent) error {
			if ev.Type == bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ATTACHED {
				select {
				case <-attached:
				default:
					close(attached)
				}
			}
			return nil
		})
	}()

	select {
	case <-writeCtx.Done():
		<-recvDone
		return fmt.Errorf("attach writer: timeout waiting for confirmation")
	case <-attached:
	}

	_, err = o.client.WriteInput(writeCtx, &bridgev1.WriteInputRequest{
		SessionId: sessionID,
		ClientId:  clientID,
		Data:      []byte(data),
	})

	// Cancel to release the writer slot, then wait for the stream to close.
	cancel()
	<-recvDone

	return err
}

// stopSession sends a force-stop to the bridge, tolerating errors.
func (o *orchestrator) stopSession(sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := o.client.StopSession(ctx, &bridgev1.StopSessionRequest{
		SessionId: sessionID,
		Force:     true,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "[orchestrator] stop error: %v\n", err)
	}
}

// errWatchTimeout signals that the watch interval elapsed normally.
var errWatchTimeout = errors.New("watch interval elapsed")
