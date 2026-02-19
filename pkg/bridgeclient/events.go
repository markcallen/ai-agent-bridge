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
	client       *Client
	sessionID    string
	subscriberID string
	afterSeq     uint64
	logger       *slog.Logger
}

// StreamEvents opens an event stream for a session with automatic reconnect.
// The returned EventStream supports Recv() which handles reconnection transparently.
func (c *Client) StreamEvents(ctx context.Context, req *bridgev1.StreamEventsRequest) (*EventStream, error) {
	afterSeq := req.AfterSeq
	if afterSeq == 0 && req.SubscriberId != "" && c.cursors != nil {
		saved, err := c.cursors.LoadCursor(ctx, req.SessionId, req.SubscriberId)
		if err == nil && saved > 0 {
			afterSeq = saved
		}
	}
	return &EventStream{
		client:       c,
		sessionID:    req.SessionId,
		subscriberID: req.SubscriberId,
		afterSeq:     afterSeq,
		logger:       slog.Default(),
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
		SessionId:    es.sessionID,
		SubscriberId: es.subscriberID,
		AfterSeq:     es.afterSeq,
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
			if es.subscriberID != "" && es.client.cursors != nil {
				if err := es.client.cursors.SaveCursor(ctx, es.sessionID, es.subscriberID, es.afterSeq); err != nil {
					es.logger.Warn("failed to persist event cursor",
						"session_id", es.sessionID,
						"subscriber_id", es.subscriberID,
						"seq", es.afterSeq,
						"error", err,
					)
				}
			}
		}

		if err := callback(event); err != nil {
			return err
		}
	}
}
