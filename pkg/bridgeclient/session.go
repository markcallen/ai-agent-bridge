package bridgeclient

import (
	"context"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
)

// StartSession creates and starts a new agent session.
func (c *Client) StartSession(ctx context.Context, req *bridgev1.StartSessionRequest) (*bridgev1.StartSessionResponse, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()

	// Auto-set JWT project scope
	c.SetProject(req.ProjectId)

	resp, err := c.rpc.StartSession(ctx, req)
	return resp, mapError(err)
}

// StopSession terminates an agent session.
func (c *Client) StopSession(ctx context.Context, req *bridgev1.StopSessionRequest) (*bridgev1.StopSessionResponse, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	resp, err := c.rpc.StopSession(ctx, req)
	return resp, mapError(err)
}

// GetSession returns information about a session.
func (c *Client) GetSession(ctx context.Context, req *bridgev1.GetSessionRequest) (*bridgev1.GetSessionResponse, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	resp, err := c.rpc.GetSession(ctx, req)
	return resp, mapError(err)
}

// ListSessions returns all sessions, optionally filtered by project.
func (c *Client) ListSessions(ctx context.Context, req *bridgev1.ListSessionsRequest) (*bridgev1.ListSessionsResponse, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	resp, err := c.rpc.ListSessions(ctx, req)
	return resp, mapError(err)
}

// SendInput sends text input to a running session.
func (c *Client) SendInput(ctx context.Context, req *bridgev1.SendInputRequest) (*bridgev1.SendInputResponse, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	resp, err := c.rpc.SendInput(ctx, req)
	return resp, mapError(err)
}

// Health checks the bridge daemon health.
func (c *Client) Health(ctx context.Context) (*bridgev1.HealthResponse, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	resp, err := c.rpc.Health(ctx, &bridgev1.HealthRequest{})
	return resp, mapError(err)
}

// ListProviders returns available providers.
func (c *Client) ListProviders(ctx context.Context) (*bridgev1.ListProvidersResponse, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	resp, err := c.rpc.ListProviders(ctx, &bridgev1.ListProvidersRequest{})
	return resp, mapError(err)
}
