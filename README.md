# AI Agent Bridge

A standalone gRPC daemon and Go SDK that provides a secure, zero-trust communication layer between control-plane systems and AI agent processes. It manages AI agent subprocess lifecycles (codex, claude, opencode) and exposes a unified API for session management, command routing, and event streaming.

## Quick Start

### Prerequisites

- [Go 1.25+](https://go.dev/dl/)
- (Optional) `protoc` with `protoc-gen-go` and `protoc-gen-go-grpc` — only needed if modifying `.proto` files

### Clone and Build

```bash
git clone https://github.com/markcallen/ai-agent-bridge.git
cd ai-agent-bridge
make build
```

This produces two binaries in `bin/`:
- `bin/bridge` — the bridge daemon
- `bin/bridge-ca` — certificate authority management CLI

### Generate Dev Certificates

```bash
./scripts/dev_certs.sh
```

This creates a full set of development certificates in `certs/`:
- CA certificate and key
- Server certificate for `localhost`
- Client certificate for testing
- Trust bundle
- Ed25519 JWT signing keypair

### Create a Dev Config

Create `config/bridge-dev.yaml`:

```yaml
server:
  listen: "127.0.0.1:9445"

tls:
  ca_bundle: "certs/ca-bundle.crt"
  cert: "certs/bridge.local.crt"
  key: "certs/bridge.local.key"

auth:
  jwt_public_keys:
    - issuer: "dev"
      key_path: "certs/jwt-signing.pub"
  jwt_audience: "bridge"
  jwt_max_ttl: "5m"

sessions:
  max_per_project: 5
  max_global: 20
  idle_timeout: "30m"
  stop_grace_period: "10s"
  event_buffer_size: 10000
  max_subscribers_per_session: 10
  subscriber_ttl: "30m"

input:
  max_size_bytes: 65536

providers:
  claude:
    binary: "./scripts/claude-print.sh"
    args: ["--print", "--verbose"]
    startup_timeout: "30s"

allowed_paths:
  - "/home"
  - "/tmp"

logging:
  level: "info"
  format: "json"
```

### Run the Bridge

```bash
bin/bridge --config config/bridge-dev.yaml
```

The daemon starts on `127.0.0.1:9445` with mTLS and JWT authentication enabled.

To run **without TLS** for quick local testing (not recommended beyond initial exploration):

```bash
bin/bridge --config config/bridge.yaml
```

The default `config/bridge.yaml` has empty cert paths, so the bridge starts in plaintext dev mode with auth disabled.

### Run Tests

```bash
make test
```

## Using the Bridge

Once the bridge daemon is running, you can interact with it via the Go SDK or `grpcurl`.

### Go SDK

Import `pkg/bridgeclient` and connect to the bridge. When running against the default `config/bridge.yaml` (no TLS, no auth), a minimal client looks like:

```go
package main

import (
	"context"
	"fmt"
	"log"

	bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
	"github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
)

func main() {
	client, err := bridgeclient.New(
		bridgeclient.WithTarget("127.0.0.1:9445"),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx := context.Background()

	// Check health
	health, err := client.Health(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Status: %s\n", health.Status)
	for _, p := range health.Providers {
		fmt.Printf("  %s: available=%v\n", p.Provider, p.Available)
	}

	// List providers
	providers, err := client.ListProviders(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range providers.Providers {
		fmt.Printf("Provider: %s (available=%v)\n", p.Provider, p.Available)
	}

	// Start a session
	resp, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
		ProjectId: "my-project",
		SessionId: "session-001",
		RepoPath:  "/home/user/repos/my-project",
		Provider:  "claude",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Session %s: %s\n", resp.SessionId, resp.Status)

	// Send input to the agent
	input, err := client.SendInput(ctx, &bridgev1.SendInputRequest{
		SessionId: "session-001",
		Text:      "review the code in main.go",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Input accepted: %v (seq=%d)\n", input.Accepted, input.Seq)

	// Stream events from the session (subscriber_id enables cursor-based resume on reconnect)
	stream, err := client.StreamEvents(ctx, &bridgev1.StreamEventsRequest{
		SessionId:    "session-001",
		SubscriberId: "my-subscriber-1",
	})
	if err != nil {
		log.Fatal(err)
	}
	stream.RecvAll(ctx, func(event *bridgev1.SessionEvent) error {
		fmt.Printf("[%s] %s: %s\n", event.Type, event.Stream, event.Text)
		return nil
	})
}
```

#### With mTLS + JWT (production / dev certs)

When connecting to a bridge running with TLS and auth enabled (e.g. `config/bridge-dev.yaml`):

```go
client, err := bridgeclient.New(
	bridgeclient.WithTarget("127.0.0.1:9445"),
	bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
		CABundlePath: "certs/ca-bundle.crt",
		CertPath:     "certs/dev-client.crt",
		KeyPath:      "certs/dev-client.key",
		ServerName:   "bridge.local",
	}),
	bridgeclient.WithJWT(bridgeclient.JWTConfig{
		PrivateKeyPath: "certs/jwt-signing.key",
		Issuer:         "dev",
		Audience:       "bridge",
	}),
)
```

### grpcurl

Install [grpcurl](https://github.com/fullstorydev/grpcurl) to interact with the bridge from the command line.

**Without TLS** (using `config/bridge.yaml`):

```bash
# Health check
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  127.0.0.1:9445 bridge.v1.BridgeService/Health

# List providers
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  127.0.0.1:9445 bridge.v1.BridgeService/ListProviders

# Start a session
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  -d '{"project_id":"my-project","session_id":"s1","repo_path":"/tmp/myrepo","provider":"claude"}' \
  127.0.0.1:9445 bridge.v1.BridgeService/StartSession

# Send input
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  -d '{"session_id":"s1","text":"hello"}' \
  127.0.0.1:9445 bridge.v1.BridgeService/SendInput

# Stream events
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  -d '{"session_id":"s1"}' \
  127.0.0.1:9445 bridge.v1.BridgeService/StreamEvents

# List sessions
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  127.0.0.1:9445 bridge.v1.BridgeService/ListSessions

# Stop a session
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  -d '{"session_id":"s1"}' \
  127.0.0.1:9445 bridge.v1.BridgeService/StopSession
```

**With mTLS** (using `config/bridge-dev.yaml`):

```bash
grpcurl \
  -cacert certs/ca-bundle.crt \
  -cert certs/dev-client.crt \
  -key certs/dev-client.key \
  -servername bridge.local \
  -import-path proto -proto bridge/v1/bridge.proto \
  127.0.0.1:9445 bridge.v1.BridgeService/Health
```

Note: `grpcurl` does not support JWT injection natively. For authenticated RPCs beyond Health (which is exempt from JWT auth), use the Go SDK or write a small Go program that mints a token.

## Project Structure

```
cmd/bridge/          Bridge daemon binary
cmd/bridge-ca/       CA and certificate management CLI
pkg/bridgeclient/    Go SDK for consumer integration
proto/bridge/v1/     Protobuf service definitions
gen/bridge/v1/       Generated protobuf Go code
internal/auth/       mTLS, JWT, and gRPC interceptors
internal/bridge/     Session supervisor, event buffer, provider registry, policy
internal/config/     YAML configuration loader
internal/pki/        CA management, cert issuance, cross-signing, verification
internal/provider/   Stdio-based provider adapters (codex, claude, opencode)
internal/server/     gRPC server implementation
config/              Default configuration files
scripts/             Development helper scripts
```

## Makefile Targets

| Target | Description |
|---|---|
| `make build` | Build both binaries (runs proto generation first) |
| `make test` | Run all tests with race detection |
| `make test-e2e` | Run dockerized end-to-end test suite |
| `make test-cover` | Run tests with coverage report |
| `make proto` | Regenerate protobuf Go code |
| `make lint` | Run golangci-lint |
| `make fmt` | Format code with gofmt and goimports |
| `make certs` | Initialize a bridge CA in `certs/` |
| `make dev-setup` | Build + generate dev certs |
| `make clean` | Remove build artifacts |

### Dockerized E2E Test

```bash
make test-e2e
```

This builds `bridge` and `test-client` containers, starts the bridge with mTLS+JWT enabled, and runs the e2e client scenario against it.

## bridge-ca Commands

```bash
bridge-ca init         # Initialize a new CA
bridge-ca issue        # Issue a server or client certificate
bridge-ca cross-sign   # Cross-sign an external CA certificate
bridge-ca bundle       # Build a trust bundle from multiple CA certs
bridge-ca jwt-keygen   # Generate Ed25519 keypair for JWT signing
bridge-ca verify       # Verify a certificate against a trust bundle
```

Run `bridge-ca <command> --help` for details on each command.

## Documentation

- [PRD.md](PRD.md) — Product requirements document
- [ARCHITECTURE.md](ARCHITECTURE.md) — System architecture and design
- [PLAN.md](PLAN.md) — Implementation plan
- [TODO.md](TODO.md) — Current task tracking
