package bridgeclient

import (
	"context"
	"testing"
	"time"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// blockingAttachStream blocks on Recv until the context is done, then returns
// the gRPC-mapped context error — exactly what the real gRPC transport does.
type blockingAttachStream struct {
	ctx context.Context
}

func (b *blockingAttachStream) Recv() (*bridgev1.AttachSessionEvent, error) {
	<-b.ctx.Done()
	return nil, status.FromContextError(b.ctx.Err()).Err()
}
func (b *blockingAttachStream) Header() (metadata.MD, error) { return nil, nil }
func (b *blockingAttachStream) Trailer() metadata.MD         { return nil }
func (b *blockingAttachStream) CloseSend() error             { return nil }
func (b *blockingAttachStream) Context() context.Context     { return b.ctx }
func (b *blockingAttachStream) SendMsg(any) error            { return nil }
func (b *blockingAttachStream) RecvMsg(any) error            { return nil }

// blockingRPCClient wraps fakeRPCClient but overrides AttachSession to return
// a blockingAttachStream that keeps the context alive.
type blockingRPCClient struct {
	fakeRPCClient
}

func (b *blockingRPCClient) AttachSession(ctx context.Context, _ *bridgev1.AttachSessionRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[bridgev1.AttachSessionEvent], error) {
	return &blockingAttachStream{ctx: ctx}, nil
}

// TestRecvAllWithTimeoutContextReturnsDeadlineExceeded demonstrates the old bug:
// when RecvAll is called with a context that has a deadline, the stream is
// terminated with DeadlineExceeded once the deadline fires.
func TestRecvAllWithTimeoutContextReturnsDeadlineExceeded(t *testing.T) {
	c := &Client{
		rpc:     &blockingRPCClient{},
		cursors: NewMemoryCursorStore(),
	}

	stream, err := c.AttachSession(context.Background(), &bridgev1.AttachSessionRequest{
		SessionId: "session-deadline-test",
	})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	recvErr := stream.RecvAll(ctx, func(*bridgev1.AttachSessionEvent) error { return nil })

	st, ok := status.FromError(recvErr)
	if !ok || st.Code() != codes.DeadlineExceeded {
		t.Errorf("want gRPC DeadlineExceeded, got %v", recvErr)
	}
}

// TestRecvAllWithCancelContextDoesNotExpire demonstrates the fix: when RecvAll
// is called with a cancel-only context (no deadline), the stream stays alive
// indefinitely and only terminates when cancel is called.
func TestRecvAllWithCancelContextDoesNotExpire(t *testing.T) {
	c := &Client{
		rpc:     &blockingRPCClient{},
		cursors: NewMemoryCursorStore(),
	}

	stream, err := c.AttachSession(context.Background(), &bridgev1.AttachSessionRequest{
		SessionId: "session-cancel-test",
	})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- stream.RecvAll(ctx, func(*bridgev1.AttachSessionEvent) error { return nil })
	}()

	// Stream must still be running after 100 ms — no deadline to fire.
	select {
	case recvErr := <-done:
		t.Errorf("RecvAll terminated early: %v", recvErr)
	case <-time.After(100 * time.Millisecond):
	}

	// Explicit cancel should end the stream cleanly (Canceled, not DeadlineExceeded).
	cancel()
	select {
	case recvErr := <-done:
		st, ok := status.FromError(recvErr)
		if ok && st.Code() == codes.DeadlineExceeded {
			t.Errorf("got DeadlineExceeded after cancel — deadline still set somewhere")
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("RecvAll did not return after cancel")
	}
}
