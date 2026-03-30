package bridgeclient

import (
	"context"
	"io"

	"github.com/google/uuid"
	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
)

// OutputStream wraps the PTY output stream for one attached client.
type OutputStream struct {
	client   *Client
	session  string
	clientID string
	afterSeq uint64
}

func (c *Client) AttachSession(ctx context.Context, req *bridgev1.AttachSessionRequest) (*OutputStream, error) {
	clientID := req.ClientId
	if clientID == "" {
		clientID = generateClientID()
	}
	afterSeq := req.AfterSeq
	if afterSeq == 0 && c.cursors != nil {
		saved, err := c.cursors.LoadCursor(ctx, req.SessionId, clientID)
		if err == nil && saved > 0 {
			afterSeq = saved
		}
	}
	return &OutputStream{
		client:   c,
		session:  req.SessionId,
		clientID: clientID,
		afterSeq: afterSeq,
	}, nil
}

func (s *OutputStream) ClientID() string { return s.clientID }

func (s *OutputStream) RecvAll(ctx context.Context, callback func(*bridgev1.AttachSessionEvent) error) error {
	stream, err := s.client.rpc.AttachSession(ctx, &bridgev1.AttachSessionRequest{
		SessionId: s.session,
		ClientId:  s.clientID,
		AfterSeq:  s.afterSeq,
	})
	if err != nil {
		return mapError(err)
	}
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if ev.Seq > s.afterSeq {
			s.afterSeq = ev.Seq
			if s.client.cursors != nil {
				_ = s.client.cursors.SaveCursor(ctx, s.session, s.clientID, s.afterSeq)
			}
		}
		if err := callback(ev); err != nil {
			return err
		}
	}
}

func generateClientID() string {
	return uuid.NewString()
}
