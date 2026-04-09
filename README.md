# AI Agent Bridge

[![CI](https://github.com/markcallen/ai-agent-bridge/actions/workflows/ci.yml/badge.svg)](https://github.com/markcallen/ai-agent-bridge/actions/workflows/ci.yml)
[![Smoke Tests](https://github.com/markcallen/ai-agent-bridge/actions/workflows/smoke.yml/badge.svg)](https://github.com/markcallen/ai-agent-bridge/actions/workflows/smoke.yml)
[![Publish](https://github.com/markcallen/ai-agent-bridge/actions/workflows/publish.yml/badge.svg)](https://github.com/markcallen/ai-agent-bridge/actions/workflows/publish.yml)
[![License](https://img.shields.io/github/license/markcallen/ai-agent-bridge)](LICENSE)
[![GitHub Release](https://img.shields.io/github/v/release/markcallen/ai-agent-bridge)](https://github.com/markcallen/ai-agent-bridge/releases)

A standalone gRPC daemon and SDK that manages AI agent subprocess lifecycles and exposes a PTY transport so any client can attach to, interact with, and replay the terminal output of a running AI agent — regardless of when it connected.

Supported providers: **Claude**, **Codex**, **OpenCode**, **Gemini**

---

## How It Works

```
Your App (Go / Node.js / Browser)
    ↕ Go SDK  or  Node.js SDK  or  raw gRPC
ai-agent-bridge daemon
    ↕ PTY
AI Agent process (claude / codex / opencode / gemini)
```

The bridge daemon spawns AI agents inside PTYs, buffers their output in a bounded ring buffer with sequence numbers, and serves a gRPC API. Clients can attach at any time, replay buffered output, and stream live PTY bytes. Authentication uses mTLS + JWT (Ed25519).

See [docs/service.md](docs/service.md) for architecture details.

---

## Quick Start (run the server)

### Prerequisites

- [Go 1.25+](https://go.dev/dl/)
- [nvm](https://github.com/nvm-sh/nvm)
- Node.js 22 (LTS) or 24 (Active LTS). Use the version in `.nvmrc`.
- (optional) `protoc` + `protoc-gen-go` + `protoc-gen-go-grpc` — only if modifying `.proto` files

### 1. Clone and configure

```bash
git clone https://github.com/markcallen/ai-agent-bridge.git
cd ai-agent-bridge
nvm install
nvm use
eval "$(env-secrets export)"
```

Store required API keys with `env-secrets`:

```bash
env-secrets set ANTHROPIC_API_KEY sk-ant-...
env-secrets set OPENAI_API_KEY sk-...
env-secrets set GEMINI_API_KEY ...
```

The expected secret names are listed in `env-secrets.example`. Do not commit `.env` files.

### 2. Start the daemon

```bash
make dev-run
```

This builds the binaries, installs the pinned AI agent CLIs into `node_modules`, generates dev TLS certificates and JWT signing keys, then starts the daemon on `127.0.0.1:9445` with mTLS + JWT.

### 3. Try an interactive session

```bash
make chat-claude     # or chat-opencode, chat-codex, chat-gemini
```

### Docker

```bash
eval "$(env-secrets export)"
make up
```

Mounts `~/repos` → `/repos` and `./certs` → `/app/certs`. The prebuilt image is available at `ghcr.io/markcallen/ai-agent-bridge`.

### Smoke Test

```bash
eval "$(env-secrets export)"
make smoke
```

This validates the repo Dockerfile and Compose stack by starting the bridge in Docker and running an authenticated gRPC health check.
It also verifies config-driven provider fallback by requesting a deliberately unavailable smoke provider and asserting the configured fallback provider is selected.

---

## Installing the Go SDK

Add the SDK to your Go module:

```bash
go get github.com/markcallen/ai-agent-bridge/pkg/bridgeclient
```

### Minimal example

```go
import (
    "context"
    "github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
    bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
)

// Connect (no TLS — for local dev with auth disabled)
client, err := bridgeclient.New(
    bridgeclient.WithTarget("127.0.0.1:9445"),
)
if err != nil { ... }
defer client.Close()

ctx := context.Background()

// Start an agent session
_, err = client.StartSession(ctx, &bridgev1.StartSessionRequest{
    ProjectId: "my-project",
    SessionId: "session-001",
    RepoPath:  "/path/to/repo",
    Provider:  "claude",
})

// Attach and stream output
stream, err := client.AttachSession(ctx, &bridgev1.AttachSessionRequest{
    SessionId: "session-001",
})
stream.RecvAll(ctx, func(ev *bridgev1.AttachSessionEvent) error {
    fmt.Print(string(ev.Payload))
    return nil
})

// Send input
client.WriteInput(ctx, &bridgev1.WriteInputRequest{
    SessionId: "session-001",
    Data:      []byte("hello\n"),
})
```

### With mTLS + JWT (production)

```go
client, err := bridgeclient.New(
    bridgeclient.WithTarget("bridge.internal:9445"),
    bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
        CABundlePath: "certs/ca-bundle.crt",
        CertPath:     "certs/client.crt",
        KeyPath:      "certs/client.key",
        ServerName:   "bridge.local",
    }),
    bridgeclient.WithJWT(bridgeclient.JWTConfig{
        PrivateKeyPath: "certs/jwt-signing.key",
        Issuer:         "my-service",
        Audience:       "bridge",
    }),
)
```

Full Go SDK reference: [docs/go-sdk.md](docs/go-sdk.md)

---

## Installing the Node.js SDK

```bash
npm install @ai-agent-bridge/client-node
```

### Next.js Pages Router

Create `pages/api/bridge.ts`:

```ts
import { createNextJsBridgeRoute } from "@ai-agent-bridge/client-node";
export default createNextJsBridgeRoute({ bridgeAddr: "localhost:9445" });
export const config = { api: { bodyParser: false } };
```

### React hook

```tsx
import { useBridgeSession } from "@ai-agent-bridge/client-node/react";

function AgentPanel() {
  const bridge = useBridgeSession("ws://localhost:3000/api/bridge");

  return (
    <>
      <button onClick={() => bridge.startSession({
        projectId: "p1",
        repoPath: "/repo",
        provider: "claude",
      })}>Start</button>
      {bridge.events.map(ev => <p key={ev.seq}>{ev.text}</p>)}
    </>
  );
}
```

Full Node.js SDK reference: [docs/node-sdk.md](docs/node-sdk.md)

---

## Using grpcurl

Install [grpcurl](https://github.com/fullstorydev/grpcurl) to call the bridge from a shell.

**With mTLS** (after `make dev-run`):

```bash
grpcurl \
  -cacert certs/ca-bundle.crt \
  -cert certs/dev-client.crt \
  -key certs/dev-client.key \
  -servername bridge.local \
  -import-path proto -proto bridge/v1/bridge.proto \
  127.0.0.1:9445 bridge.v1.BridgeService/Health
```

**Without TLS** (auth disabled):

```bash
# Health check
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  127.0.0.1:9445 bridge.v1.BridgeService/Health

# Start a session
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  -d '{"project_id":"dev","session_id":"s1","repo_path":"/tmp","provider":"claude"}' \
  127.0.0.1:9445 bridge.v1.BridgeService/StartSession

# Send input
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  -d '{"session_id":"s1","client_id":"c1","data":"aGVsbG8K"}' \
  127.0.0.1:9445 bridge.v1.BridgeService/WriteInput

# Stream output
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  -d '{"session_id":"s1","client_id":"c1"}' \
  127.0.0.1:9445 bridge.v1.BridgeService/AttachSession

# Stop a session
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  -d '{"session_id":"s1"}' \
  127.0.0.1:9445 bridge.v1.BridgeService/StopSession
```

Note: `data` is base64-encoded bytes. `grpcurl` does not support JWT injection — use the Go SDK for JWT-authenticated calls.

Full API reference: [docs/grpc-api.md](docs/grpc-api.md)

Full documentation index: [docs/README.md](docs/README.md)

---

## Providers

| Provider | Binary | Required env |
|----------|--------|--------------|
| `claude` | `./node_modules/.bin/claude` | `ANTHROPIC_API_KEY` |
| `opencode` | `./node_modules/.bin/opencode` | `OPENAI_API_KEY` |
| `codex` | `./node_modules/.bin/codex` | `OPENAI_API_KEY` |
| `gemini` | `./node_modules/.bin/gemini` | `GEMINI_API_KEY` |

Providers are configured in `config/bridge-dev.yaml`. See [docs/service.md](docs/service.md) for configuration reference.

---

## Makefile Targets

| Target | Description |
|--------|-------------|
| `make dev-run` | Build, generate dev certs, start the daemon |
| `make build` | Build `bin/bridge` and `bin/bridge-ca` |
| `make test` | Run unit tests with race detection |
| `make test-e2e` | Run the Dockerized end-to-end test suite |
| `make test-cover` | Run tests with coverage report |
| `make chat-claude` | Interactive PTY session with Claude |
| `make chat-opencode` | Interactive PTY session with OpenCode |
| `make chat-codex` | Interactive PTY session with Codex |
| `make chat-gemini` | Interactive PTY session with Gemini |
| `make proto` | Regenerate protobuf Go code (after editing `.proto`) |
| `make lint` | Run golangci-lint |
| `make fmt` | Format code with gofmt + goimports |
| `make dev-setup` | Build binaries and generate dev certificates |
| `make certs` | Initialize a bridge CA in `certs/` |
| `make clean` | Remove build artifacts |

---

## bridge-ca: Certificate and Key Management

```bash
bridge-ca init          # Initialize a new ECDSA P-384 CA
bridge-ca issue         # Issue a server or client certificate
bridge-ca cross-sign    # Cross-sign an external CA for multi-tenant trust
bridge-ca bundle        # Build a trust bundle from multiple CA certs
bridge-ca jwt-keygen    # Generate an Ed25519 keypair for JWT signing
bridge-ca verify        # Verify a certificate against a trust bundle
```

Run `bridge-ca <command> --help` for flags.

---

## Project Structure

```
cmd/bridge/                    Daemon binary
cmd/bridge-ca/                 CA and certificate management CLI
pkg/bridgeclient/              Go SDK
packages/bridge-client-node/   Node.js gRPC-to-WebSocket adapter + React hook
proto/bridge/v1/               Protobuf service definitions
gen/bridge/v1/                 Generated protobuf Go code (do not edit)
internal/auth/                 mTLS, JWT, gRPC interceptors
internal/bridge/               Session supervisor, event buffer, registry, policy
internal/config/               YAML configuration loader
internal/pki/                  CA management, cert issuance, cross-signing
internal/provider/             Stdio/PTY provider adapters
internal/server/               gRPC server implementation + rate limiting
examples/chat/                 Interactive PTY passthrough example
docs/                          Integration guides and API reference
config/                        Default configuration files
scripts/                       Development helper scripts
```

---

## Documentation

- [docs/service.md](docs/service.md) — Architecture, configuration reference, security
- [docs/go-sdk.md](docs/go-sdk.md) — Go SDK reference
- [docs/node-sdk.md](docs/node-sdk.md) — Node.js SDK and React hook reference
- [docs/grpc-api.md](docs/grpc-api.md) — Full gRPC API reference
- [docs/go-websocket-integration.md](docs/go-websocket-integration.md) — Go HTTP WebSocket integration guide
- [packages/bridge-client-node/README.md](packages/bridge-client-node/README.md) — Node.js client deep dive

---

## License

MIT License - see [LICENSE](LICENSE) file for details.
