package bridgeclient

import (
	"context"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
)

// StartSession creates and starts a new agent session.
func (c *Client) StartSession(ctx context.Context, req *bridgev1.StartSessionRequest) (*bridgev1.StartSessionResponse, error) {
	// Auto-set JWT project scope
	c.SetProject(req.ProjectId)

	var resp *bridgev1.StartSessionResponse
	err := c.invoke(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.rpc.StartSession(callCtx, req)
		return callErr
	})
	return resp, err
}

// StopSession terminates an agent session.
func (c *Client) StopSession(ctx context.Context, req *bridgev1.StopSessionRequest) (*bridgev1.StopSessionResponse, error) {
	var resp *bridgev1.StopSessionResponse
	err := c.invoke(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.rpc.StopSession(callCtx, req)
		return callErr
	})
	return resp, err
}

// GetSession returns information about a session.
func (c *Client) GetSession(ctx context.Context, req *bridgev1.GetSessionRequest) (*bridgev1.GetSessionResponse, error) {
	var resp *bridgev1.GetSessionResponse
	err := c.invoke(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.rpc.GetSession(callCtx, req)
		return callErr
	})
	return resp, err
}

// ListSessions returns all sessions, optionally filtered by project.
func (c *Client) ListSessions(ctx context.Context, req *bridgev1.ListSessionsRequest) (*bridgev1.ListSessionsResponse, error) {
	var resp *bridgev1.ListSessionsResponse
	err := c.invoke(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.rpc.ListSessions(callCtx, req)
		return callErr
	})
	return resp, err
}

// SendInput sends text input to a running session.
func (c *Client) SendInput(ctx context.Context, req *bridgev1.SendInputRequest) (*bridgev1.SendInputResponse, error) {
	var resp *bridgev1.SendInputResponse
	err := c.invoke(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.rpc.SendInput(callCtx, req)
		return callErr
	})
	return resp, err
}

// Health checks the bridge daemon health.
func (c *Client) Health(ctx context.Context) (*bridgev1.HealthResponse, error) {
	var resp *bridgev1.HealthResponse
	err := c.invoke(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.rpc.Health(callCtx, &bridgev1.HealthRequest{})
		return callErr
	})
	return resp, err
}

// ListProviders returns available providers.
func (c *Client) ListProviders(ctx context.Context) (*bridgev1.ListProvidersResponse, error) {
	var resp *bridgev1.ListProvidersResponse
	err := c.invoke(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.rpc.ListProviders(callCtx, &bridgev1.ListProvidersRequest{})
		return callErr
	})
	return resp, err
}
