package bridgeclient

import (
	"context"
	"io"
	"log/slog"
	"time"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
)

// EventStream wraps a gRPC stream with automatic reconnection support.
type EventStream struct {
	client    *Client
	sessionID string
	afterSeq  uint64
	logger    *slog.Logger
}

// StreamEvents opens an event stream for a session with automatic reconnect.
// The returned EventStream supports Recv() which handles reconnection transparently.
func (c *Client) StreamEvents(ctx context.Context, req *bridgev1.StreamEventsRequest) (*EventStream, error) {
	return &EventStream{
		client:    c,
		sessionID: req.SessionId,
		afterSeq:  req.AfterSeq,
		logger:    slog.Default(),
	}, nil
}

// RecvAll receives all events until the context is cancelled or the stream ends.
// It handles reconnection with exponential backoff automatically.
// The callback is called for each event received.
func (es *EventStream) RecvAll(ctx context.Context, callback func(*bridgev1.SessionEvent) error) error {
	backoff := 100 * time.Millisecond
	maxBackoff := 10 * time.Second

	for {
		err := es.recvOnce(ctx, callback)
		if err == nil || ctx.Err() != nil {
			return ctx.Err()
		}

		es.logger.Warn("event stream disconnected, reconnecting",
			"session_id", es.sessionID,
			"after_seq", es.afterSeq,
			"error", err,
			"backoff", backoff,
		)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (es *EventStream) recvOnce(ctx context.Context, callback func(*bridgev1.SessionEvent) error) error {
	stream, err := es.client.rpc.StreamEvents(ctx, &bridgev1.StreamEventsRequest{
		SessionId: es.sessionID,
		AfterSeq:  es.afterSeq,
	})
	if err != nil {
		return mapError(err)
	}

	for {
		event, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		if event.Seq > es.afterSeq {
			es.afterSeq = event.Seq
		}

		if err := callback(event); err != nil {
			return err
		}
	}
}
