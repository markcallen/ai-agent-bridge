package bridgeclient

import (
	"context"
	"fmt"
	"time"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client is a typed wrapper around the BridgeService gRPC client.
type Client struct {
	conn    *grpc.ClientConn
	rpc     bridgev1.BridgeServiceClient
	timeout time.Duration
	jwtCred *jwtCredentials
}

// New creates a new bridge client with the given options.
func New(opts ...Option) (*Client, error) {
	cfg := &clientConfig{
		timeout: 30 * time.Second,
	}
	for _, o := range opts {
		o(cfg)
	}

	if cfg.target == "" {
		return nil, fmt.Errorf("target address is required (use WithTarget)")
	}

	var dialOpts []grpc.DialOption

	// Transport credentials
	if cfg.mtls != nil {
		creds, err := buildTransportCredentials(cfg.mtls)
		if err != nil {
			return nil, fmt.Errorf("build tls creds: %w", err)
		}
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(creds))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Per-RPC JWT credentials
	var jwtCred *jwtCredentials
	if cfg.jwt != nil {
		var err error
		jwtCred, err = newJWTCredentials(cfg.jwt)
		if err != nil {
			return nil, fmt.Errorf("build jwt creds: %w", err)
		}
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(jwtCred))
	}

	conn, err := grpc.NewClient(cfg.target, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial bridge: %w", err)
	}

	return &Client{
		conn:    conn,
		rpc:     bridgev1.NewBridgeServiceClient(conn),
		timeout: cfg.timeout,
		jwtCred: jwtCred,
	}, nil
}

// Close releases the gRPC connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// SetProject configures the project_id for auto-minted JWTs.
func (c *Client) SetProject(projectID string) {
	if c.jwtCred != nil {
		c.jwtCred.SetProject(projectID)
	}
}

func (c *Client) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	if c.timeout > 0 {
		return context.WithTimeout(parent, c.timeout)
	}
	return parent, func() {}
}
