# AI Agent Bridge — Service Reference

> Machine-readable summary: The bridge is a gRPC daemon (`bridge.v1.BridgeService`) that spawns AI agent processes inside PTYs, buffers their output in per-session ring buffers, and streams PTY bytes to clients. Clients attach with `AttachSession`, send input with `WriteInput`, resize with `ResizeSession`. Auth: mTLS + JWT (Ed25519). Default port: `127.0.0.1:9445`.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     ai-agent-bridge daemon                  │
│                                                             │
│  ┌──────────────┐   ┌─────────────────┐   ┌─────────────┐  │
│  │ gRPC Server  │   │    Supervisor   │   │  Provider   │  │
│  │ (mTLS+JWT)   │──▶│ session lifecycle│──▶│  Adapters   │  │
│  │ rate-limited │   │ single-client   │   │ (stdio+PTY) │  │
│  └──────────────┘   │ enforcement     │   └──────┬──────┘  │
│                     └────────┬────────┘          │         │
│                              │                   ▼         │
│                     ┌────────▼────────┐   ┌──────────────┐ │
│                     │  Event Buffer   │   │ Agent Process│ │
│                     │  ring buf, seq  │   │  in PTY      │ │
│                     │  replay-to-live │   └──────────────┘ │
│                     └─────────────────┘                    │
└─────────────────────────────────────────────────────────────┘
         ▲
         │ gRPC (AttachSession / WriteInput / ResizeSession)
         │
  ┌──────┴────────┐
  │  Your App     │
  │  Go SDK  or   │
  │  Node.js SDK  │
  └───────────────┘
```

### Core Components

| Component | Location | Responsibility |
|-----------|----------|----------------|
| gRPC Server | `internal/server/` | Request routing, rate limiting, input validation |
| Supervisor | `internal/bridge/supervisor.go` | Session lifecycle state machine, single-client enforcement |
| Event Buffer | `internal/bridge/bytebuf.go` | Bounded ring buffer with sequence numbers and replay-to-live |
| Provider Adapters | `internal/provider/` | Spawn agents in PTYs, emit typed events |
| Auth | `internal/auth/` | mTLS transport + JWT per-RPC interceptors |
| Config | `internal/config/` | YAML loader with env var override |

### Session Lifecycle

```
STARTING → RUNNING → ATTACHED (client connected)
                   ↘ STOPPING → STOPPED
                              → FAILED
```

- Output is buffered from process start, regardless of whether a client is attached.
- Only one client may be attached at a time.
- If the client disconnects, the process keeps running and buffering.
- A reconnecting client passes `after_seq` to resume from where it left off.
- If the requested `after_seq` was evicted from the buffer (the ring wrapped), the server sends a `REPLAY_GAP` event before replaying from `oldest_seq`. Clients receiving this event should treat the session output as incomplete and re-render from the oldest available chunk.

### PTY Transport Model

The daemon reads raw bytes from each PTY and stores them in a bounded ring buffer. Clients receive raw bytes — they are responsible for terminal rendering. This preserves ANSI escape sequences, alternate screen buffers, and cursor movement without requiring server-side terminal emulation.

---

## Running the Daemon

### Binary

```bash
bridge -config config/bridge-dev.yaml
```

Or use `make dev-run` for the full local dev setup (builds, generates certs, starts daemon).

### Docker

```bash
docker compose up --build bridge
```

The prebuilt image is available at `ghcr.io/markcallen/ai-agent-bridge`.

```bash
docker run \
  -p 9445:9445 \
  -v ./certs:/app/certs:ro \
  -v ~/repos:/repos \
  -e ANTHROPIC_API_KEY=sk-ant-... \
  ghcr.io/markcallen/ai-agent-bridge
```

---

## Configuration Reference

Configuration is YAML. The daemon merges environment variables into provider definitions.

### Minimal example

```yaml
server:
  listen: "127.0.0.1:9445"

tls:
  ca_bundle: "certs/ca-bundle.crt"
  cert:      "certs/bridge.local.crt"
  key:       "certs/bridge.local.key"

auth:
  jwt_public_keys:
    - issuer:    "my-service"
      key_path:  "certs/jwt-signing.pub"
  jwt_audience: "bridge"
  jwt_max_ttl:  "5m"

sessions:
  max_per_project:   5
  max_global:        20
  idle_timeout:      "30m"
  stop_grace_period: "10s"
  event_buffer_size: 8388608   # bytes per session (8 MB)

input:
  max_size_bytes: 65536

rate_limits:
  global_rps:                       50
  global_burst:                     100
  start_session_per_client_rps:     1
  start_session_per_client_burst:   3
  send_input_per_session_rps:       5
  send_input_per_session_burst:     20

providers:
  - name:            claude
    binary:          "./node_modules/.bin/claude"
    args:            []
    startup_timeout: "60s"
    startup_probe:   output
    required_env:    ["ANTHROPIC_API_KEY"]
    prompt_pattern:  '(?m)(❯|>\s*$)'

logging:
  level:   "info"
  format:  "json"
  redact_patterns:
    - '(?i)(api[_-]?key|token|secret|password)\s*[:=]\s*\S+'
```

### Field reference

#### `server`
| Field | Default | Description |
|-------|---------|-------------|
| `listen` | `127.0.0.1:9445` | gRPC bind address |

#### `tls`
| Field | Description |
|-------|-------------|
| `ca_bundle` | PEM file with trusted CA certificates |
| `cert` | Server TLS certificate (PEM) |
| `key` | Server TLS private key (PEM) |

#### `auth`
| Field | Description |
|-------|-------------|
| `jwt_public_keys` | List of `{issuer, key_path}` entries. Multiple issuers are supported for key rotation. |
| `jwt_audience` | Required `aud` claim value |
| `jwt_max_ttl` | Maximum accepted token lifetime |

#### `sessions`
| Field | Description |
|-------|-------------|
| `max_per_project` | Max concurrent sessions per project ID |
| `max_global` | Max concurrent sessions across all projects |
| `idle_timeout` | Unattached session TTL |
| `stop_grace_period` | Time to wait for graceful agent exit before SIGKILL |
| `event_buffer_size` | Per-session ring buffer capacity in bytes |

#### `providers`
| Field | Description |
|-------|-------------|
| `name` | Provider name used in `StartSessionRequest.provider` |
| `binary` | Path to the agent binary |
| `args` | Extra CLI arguments |
| `startup_timeout` | Max time to wait for the process to become ready |
| `startup_probe` | `output` — wait for first PTY output |
| `required_env` | Environment variables that must be set; daemon refuses to start the provider otherwise |
| `prompt_pattern` | Regex that matches the agent's interactive prompt (used for ready detection) |

---

## Authentication

### mTLS (transport)

The daemon requires client certificates. Use `bridge-ca` to issue them:

```bash
# Initialize a CA
bridge-ca init --out certs/

# Issue a server cert
bridge-ca issue --ca certs/ca.crt --ca-key certs/ca.key \
  --type server --cn bridge.local --san bridge.local --out certs/

# Issue a client cert
bridge-ca issue --ca certs/ca.crt --ca-key certs/ca.key \
  --type client --cn my-service --out certs/

# Build a trust bundle
bridge-ca bundle --certs certs/ca.crt --out certs/ca-bundle.crt

# Generate JWT signing keypair
bridge-ca jwt-keygen --out certs/jwt-signing
```

### JWT (per-RPC)

JWTs are Ed25519-signed. The daemon verifies the `iss`, `aud`, and `exp` claims plus a custom `projectId` claim. The Go SDK mints tokens automatically using `WithJWT(...)`.

For local dev, `make dev-setup` generates all certificates and keys.

---

## Security Model

- **Zero-trust**: every RPC requires a valid client certificate and a valid JWT.
- **Project isolation**: JWT claims bind each token to a project ID; clients can only operate on their own sessions.
- **Single-client attach**: only one client may attach per session, preventing input conflicts.
- **Rate limiting**: three independent token-bucket limiters — global RPS, per-client session creation, per-session input rate.
- **Input validation**: payload size capped at `input.max_size_bytes`; session IDs must be valid UUIDs.
- **Secret redaction**: structured logs strip values matching `redact_patterns` before writing.

---

## Observability

The daemon emits structured JSON logs (configurable). Key log events:

| Event | Fields |
|-------|--------|
| Session started | `session_id`, `provider`, `project_id`, `repo_path` |
| Client attached | `session_id`, `client_id`, `after_seq` |
| Replay gap | `session_id`, `requested_seq`, `oldest_seq` |
| Input written | `session_id`, `bytes` |
| Process exited | `session_id`, `exit_code` |
| Auth failure | `reason`, `issuer` |

---

## Related

- [Go SDK reference](go-sdk.md)
- [Node.js SDK reference](node-sdk.md)
- [gRPC API reference](grpc-api.md)
- [Go WebSocket integration](go-websocket-integration.md)
