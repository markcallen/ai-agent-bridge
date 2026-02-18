# AI Agent Bridge - Product Requirements Document

## 1. Overview

The AI Agent Bridge is a standalone server and Go SDK that provides a secure, zero-trust communication layer between control-plane systems and AI agent processes running on local or remote machines.

It enables two primary consumers to orchestrate AI agents:

- **prd-manager-control-plane** - A Go HTTP API server that manages PRD projects, sessions, and agent workflows via Slack and CLI interfaces. Currently manages agent subprocesses in-process (via `internal/agent/Manager`).
- **ndara-ai-orchestrator** - A Go gRPC-based orchestrator that coordinates repo-level AI agents across machines using mTLS + JWT authentication. Dispatches tasks and streams events from agent daemons.

The bridge replaces direct in-process agent management with a networked, provider-agnostic daemon boundary that both projects can integrate via a shared Go SDK.

---

## 2. Problem Statement

### Current Limitations

1. **prd-manager-control-plane** manages agent subprocesses in-process (`exec.CommandContext`). Agent crashes can crash the API server. There is no reconnect/replay for event streams, no multi-provider support, and no remote agent capability.

2. **ndara-ai-orchestrator** has a well-designed gRPC + mTLS architecture for remote agent daemons, but the agent daemon (`agentd`) is a stub that does not actually supervise AI agent processes. It lacks provider adapters (codex, claude, opencode) and subprocess lifecycle management.

3. Neither project has a shared, reusable agent bridge. Each would need to independently solve agent subprocess management, provider abstraction, event streaming, and security.

### What the Bridge Solves

- **Failure isolation**: Agent crashes do not crash the control-plane processes.
- **Reconnect/replay**: Event streams support sequence-based resume.
- **Provider abstraction**: Codex, Claude, and OpenCode behind a single gRPC API.
- **Local + remote agents**: Same protocol for localhost and cross-network agents.
- **Zero-trust security**: Per-project CAs with cross-signing, mTLS, and short-lived JWTs.
- **Shared infrastructure**: One implementation, two consumers, consistent behavior.

---

## 3. Goals

- Provide a standalone bridge daemon that supervises AI agent subprocess lifecycles.
- Expose a gRPC API for session management, command routing, and event streaming.
- Support codex, claude, and opencode providers at launch.
- Implement zero-trust security using per-project CAs with cross-signing, mTLS, and JWTs.
- Support both local and remote agent communication from day one.
- Ship a Go SDK (`bridgeclient`) for integration by consumer projects.
- Provide durable per-session pub/sub replay so SDK clients can reconnect and receive events they missed while disconnected (while bridge process is alive).
- Provide a CLI tool for CA/cert management (`bridge-ca`).

---

## 4. Non-Goals

- Web UI or dashboard (consumers provide their own).
- Persistent event storage (in-memory ring buffer only; consumers can persist if needed).
- AI model routing or selection (consumers decide which provider to use).
- Acting as a CI/CD system.
- SDKs for languages other than Go (protobuf definitions available for future stub generation).

---

## 5. Users

- **prd-manager-control-plane** - Integrates via Go SDK to replace `internal/agent/Manager` with bridge-backed agent sessions.
- **ndara-ai-orchestrator** - Integrates via Go SDK to add real agent subprocess management behind its existing `AgentDaemon` gRPC service.
- **DevOps/Platform Engineers** - Deploy and operate bridge daemons on agent host machines.
- **Security Engineers** - Configure and audit the zero-trust PKI infrastructure.

---

## 6. Architecture

### 6.1 Components

```
                       ┌─────────────────────┐
                       │  prd-manager-       │
                       │  control-plane      │
                       │  (HTTP API)         │
                       └────────┬────────────┘
                                │ Go SDK (bridgeclient)
                                │ gRPC + mTLS + JWT
                                ▼
┌────────────────────────────────────────────────────────┐
│                   AI Agent Bridge                       │
│                   (bridge daemon)                       │
│                                                         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │ codex adapter│  │claude adapter│  │opencode adapt.│  │
│  └──────┬───────┘  └──────┬───────┘  └──────┬────────┘  │
│         │ stdio           │ stdio           │ stdio     │
│         ▼                 ▼                 ▼           │
│  ┌──────────┐      ┌──────────┐      ┌──────────┐      │
│  │ codex    │      │ claude   │      │ opencode │      │
│  │ process  │      │ process  │      │ process  │      │
│  └──────────┘      └──────────┘      └──────────┘      │
│                                                         │
│  ┌─────────────┐  ┌──────────────┐  ┌──────────────┐   │
│  │ Session     │  │ Event Ring   │  │ Provider     │   │
│  │ Supervisor  │  │ Buffer       │  │ Registry     │   │
│  └─────────────┘  └──────────────┘  └──────────────┘   │
│                                                         │
│  ┌─────────────┐  ┌──────────────┐                      │
│  │ mTLS Server │  │ JWT Verifier │                      │
│  └─────────────┘  └──────────────┘                      │
└────────────────────────────────────────────────────────┘
                                ▲
                                │ Go SDK (bridgeclient)
                                │ gRPC + mTLS + JWT
                       ┌────────┴────────────┐
                       │  ndara-ai-          │
                       │  orchestrator       │
                       │  (gRPC)             │
                       └─────────────────────┘
```

### 6.2 Deliverables

| Deliverable | Description |
|---|---|
| `cmd/bridge` | Standalone bridge daemon binary |
| `cmd/bridge-ca` | CA and certificate management CLI |
| `pkg/bridgeclient` | Go SDK for consumer integration |
| `proto/bridge/v1` | Protobuf service definitions |
| `internal/bridge` | Session supervisor, provider adapters, event buffer |
| `internal/auth` | mTLS, JWT, and zero-trust security primitives |
| `internal/pki` | CA management, cross-signing, cert generation |

---

## 7. Security Model: Zero-Trust PKI

### 7.1 Trust Architecture

Each project (prd-manager, ndara-orchestrator, ai-agent-bridge) operates its own Certificate Authority. Trust between projects is established through cross-signing.

```
Project A CA                    Project B CA
    │                               │
    ├── Project A server cert       ├── Project B server cert
    ├── Project A client cert       ├── Project B client cert
    │                               │
    └── Cross-signs Project B CA ◄──┘
        (and vice versa)
```

### 7.2 Certificate Hierarchy

```
bridge-ca (root)
├── Bridge Server Certificate
│   └── SAN: bridge host FQDN/IP
├── Bridge Client Certificates (one per consumer)
│   ├── prd-manager client cert
│   └── ndara-orchestrator client cert
└── Cross-signed CA certificates
    ├── prd-manager CA (cross-signed by bridge CA)
    └── ndara-orchestrator CA (cross-signed by bridge CA)
```

### 7.3 Authentication Layers

**Layer 1: mTLS (Transport)**
- All gRPC connections require mutual TLS.
- Server presents a certificate signed by its project CA.
- Client presents a certificate signed by its project CA.
- Both sides verify against the cross-signed trust bundle.
- No `InsecureSkipVerify` anywhere.
- Minimum TLS 1.3.

**Layer 2: JWT (Application)**
- Every RPC call includes a short-lived JWT in gRPC metadata (`authorization: Bearer <token>`).
- JWTs are signed with Ed25519 keys (not HS256 shared secrets).
- Claims include: `sub` (caller identity), `project_id`, `aud` (bridge), `iat`, `exp`.
- TTL: 5 minutes maximum.
- Bridge verifies signature, expiry, audience, and issuer.

**Layer 3: Authorization (Policy)**
- Per-session authorization: caller must have `project_id` claim matching the session's project.
- `repo_path` validation: must be under a configured allowed-paths list.
- Per-project and global session limits enforced.
- Provider capability checks before session start.

### 7.4 Key Management

- Ed25519 keypairs for JWT signing (one per consumer project).
- RSA 4096 or ECDSA P-384 for TLS certificates.
- CA keys stored encrypted at rest (passphrase-protected PEM).
- Certificate rotation: certs expire after 90 days; automated renewal via `bridge-ca renew`.
- Revocation: CRL distribution point served by bridge daemon.

### 7.5 Defense in Depth

- Bridge binds to configurable interface (default: private/localhost).
- Rate limiting on session creation and command input.
- Input size limits (max command text size).
- Secret redaction in event streams and logs.
- Audit logging of all authentication and authorization decisions.
- No plaintext traffic under any configuration.

---

## 8. gRPC API Contract (v1)

### 8.1 Service Definition

```protobuf
syntax = "proto3";
package bridge.v1;

service BridgeService {
  // Session lifecycle
  rpc StartSession(StartSessionRequest) returns (StartSessionResponse);
  rpc StopSession(StopSessionRequest) returns (StopSessionResponse);
  rpc GetSession(GetSessionRequest) returns (GetSessionResponse);
  rpc ListSessions(ListSessionsRequest) returns (ListSessionsResponse);

  // Command routing
  rpc SendInput(SendInputRequest) returns (SendInputResponse);

  // Event streaming
  rpc StreamEvents(StreamEventsRequest) returns (stream SessionEvent);

  // Health
  rpc Health(HealthRequest) returns (HealthResponse);

  // Provider discovery
  rpc ListProviders(ListProvidersRequest) returns (ListProvidersResponse);
}
```

### 8.2 Session Lifecycle

**StartSession**
```
Request:
  project_id: string (required)
  session_id: string (required, caller-generated UUID)
  repo_path: string (required, absolute path on bridge host)
  provider: string (required: "codex" | "claude" | "opencode")
  agent_opts: map<string, string> (optional, provider-specific)

Response:
  session_id: string
  status: SessionStatus (STARTING | RUNNING | FAILED)
  created_at: google.protobuf.Timestamp
```

**StopSession**
```
Request:
  session_id: string (required)
  force: bool (optional, skip graceful shutdown)

Response:
  status: SessionStatus (STOPPING | STOPPED)
```

**GetSession**
```
Request:
  session_id: string

Response:
  session_id: string
  project_id: string
  provider: string
  status: SessionStatus
  created_at: google.protobuf.Timestamp
  stopped_at: google.protobuf.Timestamp (if applicable)
  error: string (if FAILED)
```

### 8.3 Command Routing

**SendInput**
```
Request:
  session_id: string (required)
  text: string (required, max 64KB)
  idempotency_key: string (optional)

Response:
  accepted: bool
  seq: uint64 (assigned sequence number)
```

### 8.4 Event Streaming

**StreamEvents**
```
Request:
  session_id: string (required)
  after_seq: uint64 (optional, resume from sequence)

Response (stream):
  SessionEvent:
    seq: uint64
    timestamp: google.protobuf.Timestamp
    session_id: string
    project_id: string
    provider: string
    type: EventType
    stream: string ("system" | "stdout" | "stderr")
    text: string
    done: bool
    error: string
```

**EventType enum:**
- `SESSION_STARTED`
- `SESSION_STOPPED`
- `SESSION_FAILED`
- `STDOUT`
- `STDERR`
- `INPUT_RECEIVED`
- `BUFFER_OVERFLOW`

### 8.5 Health

**Health**
```
Request: (empty)

Response:
  status: string ("serving" | "not_serving")
  providers: repeated ProviderHealth
    provider: string
    available: bool
    error: string
```

---

## 9. Provider Adapter Contract

### 9.1 Interface

```go
type Provider interface {
    ID() string
    Start(ctx context.Context, cfg SessionConfig) (SessionHandle, error)
    Send(handle SessionHandle, text string) error
    Stop(handle SessionHandle) error
    Events(handle SessionHandle) <-chan Event
    Health(ctx context.Context) error
}

type SessionConfig struct {
    ProjectID  string
    SessionID  string
    RepoPath   string
    Options    map[string]string
}

type SessionHandle interface {
    ID() string
    PID() int
}
```

### 9.2 Providers at Launch

| Provider | Binary | Communication | Notes |
|---|---|---|---|
| `codex` | `codex` | stdio (stdin/stdout/stderr) | OpenAI Codex CLI |
| `claude` | `claude` | stdio (stdin/stdout/stderr) | Anthropic Claude CLI |
| `opencode` | `opencode` | stdio (stdin/stdout/stderr) | OpenCode CLI |

Each adapter:
- Spawns the provider binary as a child process.
- Pipes stdin for input, reads stdout/stderr for events.
- Monitors process health (PID alive check).
- Handles graceful shutdown (SIGTERM, then SIGKILL after timeout).
- Reports provider availability via `Health()`.

---

## 10. Event Model

### 10.1 Event Envelope

```go
type Event struct {
    Seq       uint64    // monotonic per session
    Timestamp time.Time
    ProjectID string
    SessionID string
    Provider  string
    Type      EventType
    Stream    string    // "system", "stdout", "stderr"
    Text      string
    Done      bool
    Error     string
}
```

### 10.2 Ring Buffer

- Bounded ring buffer per session (default: 10,000 events).
- When buffer is full, oldest events are dropped and a `BUFFER_OVERFLOW` event is emitted.
- `StreamEvents` with `after_seq` replays from buffer, then switches to live streaming.
- Buffer is released when session is removed (after stop + configurable retention period).

### 10.3 Durable SDK Replay Requirement

- The bridge must maintain a per-session outbound event queue that continues to enqueue agent events even when no SDK stream is connected.
- Delivery semantics are at-least-once per `(project_id, session_id, subscriber_id)`.
- The SDK must identify itself with a stable `subscriber_id` and resume using last acknowledged sequence.
- On reconnect, the bridge replays all queued events with `seq > ack_seq` in order, then switches to live tailing.
- If queued events exceed retention capacity, bridge emits `BUFFER_OVERFLOW` and resumes from the earliest retained sequence.
- Scope: guaranteed replay is required while the bridge daemon remains running and session is active; restart durability remains out of scope for this phase.

---

## 11. Go SDK (`bridgeclient`)

### 11.1 Package Structure

```
pkg/bridgeclient/
├── client.go          // Main client type
├── options.go         // Client configuration
├── session.go         // Session operations
├── events.go          // Event subscription
├── auth.go            // mTLS + JWT credential setup
└── errors.go          // Typed errors
```

### 11.2 Client API

```go
// Create a client with zero-trust credentials
client, err := bridgeclient.New(
    bridgeclient.WithTarget("bridge.example.com:9445"),
    bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
        CACertPath: "certs/ca-bundle.crt",  // includes cross-signed CAs
        CertPath:   "certs/client.crt",
        KeyPath:    "certs/client.key",
    }),
    bridgeclient.WithJWT(bridgeclient.JWTConfig{
        PrivateKeyPath: "certs/jwt-signing.key",
        Issuer:         "prd-manager",
        Audience:       "bridge",
    }),
)

// Start a session
resp, err := client.StartSession(ctx, &bridge.StartSessionRequest{
    ProjectId: "my-project",
    SessionId: uuid.NewString(),
    RepoPath:  "/repos/my-project",
    Provider:  "claude",
})

// Send input
_, err = client.SendInput(ctx, &bridge.SendInputRequest{
    SessionId: sessionID,
    Text:      "review the code in main.go",
})

// Stream events (with reconnect/resume)
stream, err := client.StreamEvents(ctx, &bridge.StreamEventsRequest{
    SessionId: sessionID,
    AfterSeq:  lastSeenSeq,
})
for {
    event, err := stream.Recv()
    if err != nil { break }
    // process event
}
```

### 11.3 Built-in Resilience

- Automatic retry with exponential backoff for transient failures.
- JWT auto-renewal before expiry.
- Connection keepalive and health checking.
- Configurable timeouts per operation.
- Subscriber cursor tracking (`subscriber_id`, `ack_seq`) for reconnect + missed-event replay.

---

## 12. CLI Tool: `bridge-ca`

### 12.1 Commands

```bash
# Initialize a new CA for this project
bridge-ca init --name "ai-agent-bridge" --out certs/

# Generate server certificate for the bridge daemon
bridge-ca issue --type server --cn "bridge.example.com" \
    --san "bridge.example.com,192.168.1.10" \
    --ca certs/ca.crt --ca-key certs/ca.key \
    --out certs/bridge

# Generate client certificate for a consumer project
bridge-ca issue --type client --cn "prd-manager" \
    --ca certs/ca.crt --ca-key certs/ca.key \
    --out certs/prd-manager-client

# Cross-sign another project's CA
bridge-ca cross-sign \
    --signer-ca certs/ca.crt --signer-key certs/ca.key \
    --target-ca ../prd-manager-control-plane/certs/ca.crt \
    --out certs/prd-manager-ca-cross-signed.crt

# Build a trust bundle (own CA + all cross-signed CAs)
bridge-ca bundle \
    --ca certs/ca.crt \
    --cross-signed certs/prd-manager-ca-cross-signed.crt \
    --cross-signed certs/ndara-ca-cross-signed.crt \
    --out certs/ca-bundle.crt

# Generate Ed25519 keypair for JWT signing
bridge-ca jwt-keygen --out certs/jwt-signing

# Renew expiring certificates
bridge-ca renew --cert certs/bridge.crt --ca certs/ca.crt --ca-key certs/ca.key

# Verify a certificate chain
bridge-ca verify --cert certs/bridge.crt --bundle certs/ca-bundle.crt
```

---

## 13. Runtime/Policy Configuration

```yaml
# bridge.yaml
server:
  listen: "0.0.0.0:9445"

tls:
  ca_bundle: "certs/ca-bundle.crt"
  cert: "certs/bridge.crt"
  key: "certs/bridge.key"

auth:
  jwt_public_keys:
    - issuer: "prd-manager"
      key_path: "certs/prd-manager-jwt.pub"
    - issuer: "ndara-orchestrator"
      key_path: "certs/ndara-jwt.pub"
  jwt_audience: "bridge"
  jwt_max_ttl: "5m"

sessions:
  max_per_project: 5
  max_global: 20
  idle_timeout: "30m"
  stop_grace_period: "10s"
  event_buffer_size: 10000

input:
  max_size_bytes: 65536

providers:
  codex:
    binary: "codex"
    args: ["--quiet"]
    startup_timeout: "30s"
  claude:
    binary: "claude"
    args: ["--print", "--verbose"]
    startup_timeout: "30s"
  opencode:
    binary: "opencode"
    args: []
    startup_timeout: "30s"

allowed_paths:
  - "/home/*/repos"
  - "/opt/projects"

logging:
  level: "info"
  format: "json"
  redact_patterns:
    - "(?i)(api[_-]?key|token|secret|password)\\s*[:=]\\s*\\S+"
```

---

## 14. Integration Plans

### 14.1 prd-manager-control-plane Integration

**Current state**: `internal/agent/Manager` directly spawns agent subprocesses via `exec.CommandContext`. Events are emitted in-process via callback.

**Target state**: Replace `agent.Manager` usage in `httpapi/server.go` with `bridgeclient.Client`. The bridge client connects to a bridge daemon (local or remote) over gRPC+mTLS+JWT.

Changes required:
1. Add `pkg/bridgeclient` as a Go module dependency.
2. Add bridge configuration to server config (`bridge_target`, cert paths, JWT key path).
3. Replace `s.agentManager.Start(...)` calls with `bridgeClient.StartSession(...)`.
4. Replace `s.agentManager.Send(...)` calls with `bridgeClient.SendInput(...)`.
5. Replace `s.agentManager.Stop(...)` calls with `bridgeClient.StopSession(...)`.
6. Replace `s.agentManager.SetEventHandler(...)` with a goroutine calling `bridgeClient.StreamEvents(...)` and forwarding events to the existing `onAgentEvent` handler.
7. Add `provider` field to session-related API requests.
8. Keep existing Slack/CLI fan-out logic unchanged.

### 14.2 ndara-ai-orchestrator Integration

**Current state**: `agentd` implements the `AgentDaemon` gRPC service with stub handlers. `RunTask` logs the request but does not actually execute agent tasks. `StreamEvents` sends hardcoded demo events.

**Target state**: `agentd` uses `bridgeclient.Client` to connect to a local or remote bridge daemon. `RunTask` maps to `StartSession` + `SendInput`. `StreamEvents` maps to bridge `StreamEvents`.

Changes required:
1. Add `pkg/bridgeclient` as a Go module dependency.
2. In `agentd/main.go`, create a `bridgeclient.Client` connected to the local bridge daemon.
3. `RunTask` handler: call `bridgeClient.StartSession(...)` with the task's repo context, then `bridgeClient.SendInput(...)` with the task text.
4. `StreamEvents` handler: proxy `bridgeClient.StreamEvents(...)` to the caller, mapping bridge events to the existing `orch.v1.Event` protobuf.
5. `CancelRun` handler: call `bridgeClient.StopSession(...)`.
6. Reuse existing mTLS + JWT auth layers (the bridge has its own independent auth; `agentd` authenticates to the bridge as a separate trust boundary).

---

## 15. Success Criteria

1. Both consumer projects can start, command, and stop codex/claude/opencode sessions through the bridge.
2. Agent subprocess crashes do not affect the bridge daemon or consumer processes.
3. Event streams support reconnect/replay via sequence offsets.
4. All communication is encrypted with mTLS; all operations require valid JWTs.
5. Cross-signed CA trust enables secure communication between independently managed projects.
6. Session limits and policy guards are enforced.
7. Provider unavailability is detected and reported gracefully.
8. SDK reconnect receives all unseen queued session events in order (or explicit overflow signal if retention is exceeded).

---

## 16. Future Scope (Deferred)

- Additional providers: `gemini`, `droid`
- Persistent event storage (SQLite backend)
- SDKs for Python, TypeScript (generated from protobuf)
- SPIFFE/SPIRE integration for workload identity
- Web UI for bridge status and session management
- CRL/OCSP for real-time certificate revocation
- Multi-bridge clustering and session migration
- Policy-as-code (OPA/Rego) for authorization decisions
