package bridgeclient

import (
	"context"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
)

func (c *Client) StartSession(ctx context.Context, req *bridgev1.StartSessionRequest) (*bridgev1.StartSessionResponse, error) {
	c.SetProject(req.ProjectId)
	var resp *bridgev1.StartSessionResponse
	err := c.invoke(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.rpc.StartSession(callCtx, req)
		return callErr
	})
	return resp, err
}

func (c *Client) StopSession(ctx context.Context, req *bridgev1.StopSessionRequest) (*bridgev1.StopSessionResponse, error) {
	var resp *bridgev1.StopSessionResponse
	err := c.invoke(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.rpc.StopSession(callCtx, req)
		return callErr
	})
	return resp, err
}

func (c *Client) GetSession(ctx context.Context, req *bridgev1.GetSessionRequest) (*bridgev1.GetSessionResponse, error) {
	var resp *bridgev1.GetSessionResponse
	err := c.invoke(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.rpc.GetSession(callCtx, req)
		return callErr
	})
	return resp, err
}

func (c *Client) ListSessions(ctx context.Context, req *bridgev1.ListSessionsRequest) (*bridgev1.ListSessionsResponse, error) {
	var resp *bridgev1.ListSessionsResponse
	err := c.invoke(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.rpc.ListSessions(callCtx, req)
		return callErr
	})
	return resp, err
}

func (c *Client) WriteInput(ctx context.Context, req *bridgev1.WriteInputRequest) (*bridgev1.WriteInputResponse, error) {
	var resp *bridgev1.WriteInputResponse
	err := c.invoke(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.rpc.WriteInput(callCtx, req)
		return callErr
	})
	return resp, err
}

func (c *Client) ResizeSession(ctx context.Context, req *bridgev1.ResizeSessionRequest) (*bridgev1.ResizeSessionResponse, error) {
	var resp *bridgev1.ResizeSessionResponse
	err := c.invoke(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.rpc.ResizeSession(callCtx, req)
		return callErr
	})
	return resp, err
}

func (c *Client) Health(ctx context.Context) (*bridgev1.HealthResponse, error) {
	var resp *bridgev1.HealthResponse
	err := c.invoke(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.rpc.Health(callCtx, &bridgev1.HealthRequest{})
		return callErr
	})
	return resp, err
}

func (c *Client) ListProviders(ctx context.Context) (*bridgev1.ListProvidersResponse, error) {
	var resp *bridgev1.ListProvidersResponse
	err := c.invoke(ctx, func(callCtx context.Context) error {
		var callErr error
		resp, callErr = c.rpc.ListProviders(callCtx, &bridgev1.ListProvidersRequest{})
		return callErr
	})
	return resp, err
}
