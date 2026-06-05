package main

import (
	"fmt"
	"time"

	"github.com/markcallen/ai-agent-bridge/internal/localserver"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

// connectClient discovers the local server and returns a connected
// bridgeclient.Client. It auto-detects whether the server is running in
// local (insecure) or secure (mTLS+JWT) mode and configures credentials
// accordingly.
func connectClient(stateDir string, timeout time.Duration) (*bridgeclient.Client, error) {
	if stateDir == "" {
		stateDir = localserver.StateDir()
	}

	target, mode := localserver.DiscoverTarget(stateDir)
	if target == "" {
		return nil, fmt.Errorf("no ai-agent-bridge server running")
	}

	return dialClient(target, mode, stateDir, timeout)
}

// dialClient creates a bridgeclient for the given target and mode.
func dialClient(target string, mode localserver.ServerMode, stateDir string, timeout time.Duration) (*bridgeclient.Client, error) {
	var opts []bridgeclient.Option
	opts = append(opts, bridgeclient.WithTarget(target))
	if timeout > 0 {
		opts = append(opts, bridgeclient.WithTimeout(timeout))
	}

	if mode == localserver.ModeSecure {
		mat := localserver.LoadPKIMaterial(stateDir)
		opts = append(opts,
			bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
				CABundlePath: mat.CABundlePath,
				CertPath:     mat.LocalClientCert,
				KeyPath:      mat.LocalClientKey,
				ServerName:   "server",
			}),
			bridgeclient.WithJWT(bridgeclient.JWTConfig{
				PrivateKeyPath: mat.JWTSigningKey,
				Issuer:         "local",
				Audience:       "bridge",
				TTL:            5 * time.Minute,
			}),
		)
	}

	client, err := bridgeclient.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("connect to server (%s mode): %w", mode, err)
	}
	return client, nil
}
