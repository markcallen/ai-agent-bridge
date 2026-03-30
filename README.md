# AI Agent Bridge

[![Smoke Tests](https://github.com/markcallen/ai-agent-bridge/actions/workflows/smoke.yml/badge.svg)](https://github.com/markcallen/ai-agent-bridge/actions/workflows/smoke.yml)

A standalone gRPC daemon and Go SDK that provides a secure, zero-trust communication layer between control-plane systems and AI agent processes. It manages AI agent subprocess lifecycles and exposes a PTY transport so a client can see and interact with the same terminal UI the agent would show locally.

## Quick Start

### Prerequisites

- [Go 1.25+](https://go.dev/dl/)
- `npm`
- (Optional) `protoc` with `protoc-gen-go` and `protoc-gen-go-grpc` — only needed if modifying `.proto` files

### 1. Clone and configure

```bash
git clone https://github.com/markcallen/ai-agent-bridge.git
cd ai-agent-bridge
cp .env.example .env
```

Edit `.env` and add your API keys:

```bash
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
GEMINI_API_KEY=...
```

### 2. Start the bridge

```bash
make dev-run
```

This builds the binaries, installs the pinned local AI-agent CLIs into `node_modules`, generates dev TLS certificates and JWT signing keys, then starts the bridge daemon on `127.0.0.1:9445` with mTLS and JWT authentication.

### 3. Run the interactive PTY example

In a second terminal:

```bash
make chat-example
```

---

## Running the Examples

### `chat` — interactive PTY passthrough session

Opens a live PTY session against the remote bridge. The local terminal shows the same screen the provider renders on the server, and your keystrokes are written directly back to the remote PTY.

```bash
make chat-example
```

| Variable | Default | Description |
|---|---|---|
| `CHAT_PROVIDER` | `claude` | Provider name from config |
| `CHAT_REPO` | `$(PWD)` | Repo path passed to the agent |

---

## Providers

Providers are configured in `config/bridge-dev.yaml` and resolved from pinned local binaries in `node_modules/.bin`:

| Provider | Binary | Mode | Required env |
|---|---|---|---|
| `claude` | `./node_modules/.bin/claude` | PTY interactive | `ANTHROPIC_API_KEY` |
| `opencode` | `./node_modules/.bin/opencode` | PTY interactive | `OPENAI_API_KEY` |
| `gemini` | `./node_modules/.bin/gemini` | PTY interactive | `GEMINI_API_KEY` |

---

## Using from a Browser (Node.js + React)

Browsers can't speak gRPC directly. The `packages/bridge-client-node` package provides a Node.js adapter that sits between browser clients and the bridge daemon:

```
React App (Browser)
    ↕ WebSocket (JSON protocol)
Next.js or Go HTTP server
    ↕ gRPC
ai-agent-bridge daemon
```

### Node.js quick start

```bash
npm install @ai-agent-bridge/client-node
```

**Next.js Pages Router** — create `pages/api/bridge.ts`:

```ts
import { createNextJsBridgeRoute } from "@ai-agent-bridge/client-node";
export default createNextJsBridgeRoute({ bridgeAddr: "localhost:9445" });
export const config = { api: { bodyParser: false } };
```

**React hook**:

```tsx
import { useBridgeSession } from "@ai-agent-bridge/client-node/react";

function AgentPanel() {
  const bridge = useBridgeSession("ws://localhost:3000/api/bridge");

  return (
    <>
      <button onClick={() => bridge.startSession({ projectId: "p1", repoPath: "/repo", provider: "claude" })}>
        Start
      </button>
      {bridge.events.map((ev) => <p key={ev.seq}>{ev.text}</p>)}
    </>
  );
}
```

See [`packages/bridge-client-node/README.md`](packages/bridge-client-node/README.md) for the full API, App Router custom server setup, and the Go HTTP WebSocket integration guide at [`docs/go-websocket-integration.md`](docs/go-websocket-integration.md).

---

## Using the Go SDK

Import `pkg/bridgeclient` to connect from your own Go program.

### Without TLS (plain dev mode)

```go
client, err := bridgeclient.New(
    bridgeclient.WithTarget("127.0.0.1:9445"),
)
```

### With mTLS + JWT

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

### Basic usage

```go
ctx := context.Background()

// Start a session
_, err = client.StartSession(ctx, &bridgev1.StartSessionRequest{
    ProjectId: "my-project",
    SessionId: "session-001",
    RepoPath:  "/path/to/repo",
    Provider:  "claude",
})
```

See `examples/chat/main.go` for the full working example.

---

## Using grpcurl

Install [grpcurl](https://github.com/fullstorydev/grpcurl) to call the bridge from the shell.

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

**Without TLS** (bridge started with auth disabled):

```bash
# Health check
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  127.0.0.1:9445 bridge.v1.BridgeService/Health

# Start a session
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  -d '{"project_id":"dev","session_id":"s1","repo_path":"/tmp","provider":"claude-chat"}' \
  127.0.0.1:9445 bridge.v1.BridgeService/StartSession

# Send input
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  -d '{"session_id":"s1","text":"hello"}' \
  127.0.0.1:9445 bridge.v1.BridgeService/SendInput

# Stream events
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  -d '{"session_id":"s1"}' \
  127.0.0.1:9445 bridge.v1.BridgeService/StreamEvents

# Stop a session
grpcurl -plaintext -import-path proto -proto bridge/v1/bridge.proto \
  -d '{"session_id":"s1"}' \
  127.0.0.1:9445 bridge.v1.BridgeService/StopSession
```

Note: `grpcurl` does not support JWT injection. For JWT-authenticated RPCs use the Go SDK.

---

## Makefile Targets

| Target | Description |
|---|---|
| `make dev-run` | Build, generate dev certs, and start the bridge (quickest path) |
| `make build` | Build `bin/bridge` and `bin/bridge-ca` |
| `make test` | Run all unit tests with race detection |
| `make test-e2e` | Run the dockerized end-to-end test suite |
| `make test-cover` | Run tests with coverage report |
| `make chat-example` | Run the interactive PTY example against the local bridge |
| `make proto` | Regenerate protobuf Go code |
| `make lint` | Run golangci-lint |
| `make fmt` | Format code with gofmt and goimports |
| `make dev-setup` | Build binaries and generate dev certificates |
| `make certs` | Initialize a bridge CA in `certs/` |
| `make clean` | Remove build artifacts |

### Dockerized E2E tests

```bash
make test-e2e
```

Builds `bridge` and `test-client` containers, starts the bridge with mTLS+JWT, and runs the full e2e scenario against it (requires `ANTHROPIC_API_KEY` in the environment).

---

## Project Structure

```
cmd/bridge/                    Bridge daemon binary
cmd/bridge-ca/                 CA and certificate management CLI
pkg/bridgeclient/              Go SDK for consumer integration
packages/bridge-client-node/   Node.js gRPC→WebSocket adapter + React hook
proto/bridge/v1/               Protobuf service definitions
gen/bridge/v1/                 Generated protobuf Go code
internal/auth/                 mTLS, JWT, and gRPC interceptors
internal/bridge/               Session supervisor, event buffer, provider registry, policy
internal/config/               YAML configuration loader and env var validation
internal/pki/                  CA management, cert issuance, cross-signing, verification
internal/provider/             Stdio/PTY-based provider adapters
internal/server/               gRPC server implementation
examples/chat/                 Interactive PTY passthrough example
docs/                          Integration guides
config/                        Default configuration files
scripts/                       Development helper scripts
```

---

## bridge-ca Commands

```bash
bridge-ca init         # Initialize a new CA
bridge-ca issue        # Issue a server or client certificate
bridge-ca cross-sign   # Cross-sign an external CA certificate
bridge-ca bundle       # Build a trust bundle from multiple CA certs
bridge-ca jwt-keygen   # Generate Ed25519 keypair for JWT signing
bridge-ca verify       # Verify a certificate against a trust bundle
```

Run `bridge-ca <command> --help` for details.

---

## Documentation

- [PRD.md](PRD.md) — Product requirements document
- [ARCHITECTURE.md](ARCHITECTURE.md) — System architecture and design
- [PLAN.md](PLAN.md) — Implementation plan
- [TODO.md](TODO.md) — Current task tracking
- [packages/bridge-client-node/README.md](packages/bridge-client-node/README.md) — Node.js client and React hook
- [docs/go-websocket-integration.md](docs/go-websocket-integration.md) — Go HTTP WebSocket integration guide
