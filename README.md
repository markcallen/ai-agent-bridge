# AI Agent Bridge

A standalone gRPC daemon and Go SDK that provides a secure, zero-trust communication layer between control-plane systems and AI agent processes. It manages AI agent subprocess lifecycles (Claude Code, Codex, OpenCode, Gemini) and exposes a unified API for session management, command routing, and event streaming.

## Quick Start

### Prerequisites

- [Go 1.25+](https://go.dev/dl/)
- One or more AI agent CLIs installed and on `PATH`:
  - `claude` — [Claude Code](https://claude.ai/code)
  - `codex` — [OpenAI Codex CLI](https://github.com/openai/codex)
  - `opencode` — [OpenCode](https://opencode.ai)
  - `gemini` — [Gemini CLI](https://github.com/google-gemini/gemini-cli)
- (Optional) `protoc` with `protoc-gen-go` and `protoc-gen-go-grpc` — only needed if modifying `.proto` files

### 1. Clone and configure

```bash
git clone https://github.com/markcallen/ai-agent-bridge.git
cd ai-agent-bridge
cp .env.example .env
```

Edit `.env` and add your API keys:

```bash
ANTHROPIC_API_KEY=sk-ant-...   # required for claude-chat provider
# OPENAI_API_KEY=sk-...        # required for codex / opencode providers
```

### 2. Start the bridge

```bash
make dev-run
```

This single command builds the binaries, generates dev TLS certificates and JWT signing keys, then starts the bridge daemon on `127.0.0.1:9445` with mTLS and JWT authentication.

### 3. Run a prompt

In a second terminal:

```bash
make runprompt-example RUNPROMPT_PROMPT="list 5 important TODOs in this codebase"
```

Or run an interactive chat session:

```bash
make chat-example
```

---

## Running the Examples

### `runprompt` — single prompt, streamed response

Sends one prompt to an agent and exits when the response is complete.

```bash
make runprompt-example \
  RUNPROMPT_AGENT=claude-chat \
  RUNPROMPT_DIR=/path/to/your/repo \
  RUNPROMPT_PROMPT="explain the main entry point"
```

| Variable | Default | Description |
|---|---|---|
| `RUNPROMPT_AGENT` | `claude-chat` | Provider name from config |
| `RUNPROMPT_DIR` | `$(PWD)` | Repo path passed to the agent |
| `RUNPROMPT_PROMPT` | *(required)* | Prompt to send |

### `chat` — interactive multi-turn session

Opens a readline prompt loop. Type your messages and press Enter; responses stream back in real time.

```bash
make chat-example
```

| Variable | Default | Description |
|---|---|---|
| `CHAT_PROVIDER` | `claude-chat` | Provider name from config |
| `CHAT_REPO` | `$(PWD)` | Repo path passed to the agent |

---

## Providers

Providers are configured in `config/bridge-dev.yaml`. The `claude-chat` provider is enabled out of the box:

| Provider | Binary | Mode | Required env |
|---|---|---|---|
| `claude-chat` | `claude` | stream-json SDK mode (multi-turn) | `ANTHROPIC_API_KEY` |
| `codex` | `codex` | PTY interactive | `OPENAI_API_KEY` |
| `opencode` | `opencode` | PTY interactive | `OPENAI_API_KEY` |
| `gemini` | `gemini` | PTY interactive | — |

The bridge emits `AGENT_READY` when an agent is ready for input and `RESPONSE_COMPLETE` when it finishes responding, so client code does not need to rely on idle timers.

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
    Provider:  "claude-chat",
})

// Send a prompt
client.SendInput(ctx, &bridgev1.SendInputRequest{
    SessionId: "session-001",
    Text:      "review the code in main.go",
})

// Stream events
stream, _ := client.StreamEvents(ctx, &bridgev1.StreamEventsRequest{
    SessionId:    "session-001",
    SubscriberId: "my-subscriber",  // enables cursor-based resume on reconnect
})
stream.RecvAll(ctx, func(ev *bridgev1.SessionEvent) error {
    switch ev.Type {
    case bridgev1.EventType_EVENT_TYPE_STDOUT:
        fmt.Print(ev.Text)
    case bridgev1.EventType_EVENT_TYPE_RESPONSE_COMPLETE:
        // agent finished responding — safe to send next prompt
    case bridgev1.EventType_EVENT_TYPE_SESSION_STOPPED:
        // session ended
    }
    return nil
})
```

See `examples/chat/main.go` and `examples/runprompt/main.go` for full working examples.

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
| `make chat-example` | Run the interactive chat example against the local bridge |
| `make runprompt-example` | Run the single-prompt example (set `RUNPROMPT_PROMPT=`) |
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
cmd/bridge/          Bridge daemon binary
cmd/bridge-ca/       CA and certificate management CLI
pkg/bridgeclient/    Go SDK for consumer integration
proto/bridge/v1/     Protobuf service definitions
gen/bridge/v1/       Generated protobuf Go code
internal/auth/       mTLS, JWT, and gRPC interceptors
internal/bridge/     Session supervisor, event buffer, provider registry, policy
internal/config/     YAML configuration loader and env var validation
internal/pki/        CA management, cert issuance, cross-signing, verification
internal/provider/   Stdio/PTY-based provider adapters
internal/server/     gRPC server implementation
examples/chat/       Interactive multi-turn chat example
examples/runprompt/  Single-prompt streaming example
config/              Default configuration files
scripts/             Development helper scripts
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
