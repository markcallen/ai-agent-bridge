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
- Provide a CLI tool for CA/cert management (`ai-agent-bridge-ca`).

---

## 4. Non-Goals

- Web UI or dashboard (consumers provide their own).
- Persistent event storage (in-memory ring buffer only; consumers can persist if needed).
- AI model routing or selection (consumers decide which provider to use).
- Acting as a CI/CD system.
- SDKs for languages other than Go and Node.js (protobuf definitions available for future stub generation).

---

## 5. Users

- **prd-manager-control-plane** - Integrates via Go SDK to replace `internal/agent/Manager` with bridge-backed agent sessions.
- **ndara-ai-orchestrator** - Integrates via Go SDK to add real agent subprocess management behind its existing `AgentDaemon` gRPC service.
- **Web Application Developers** - Use `packages/bridge-client-node` and the `useBridgeSession` React hook to embed agent sessions in browser-based UIs without needing to speak gRPC directly.
- **DevOps/Platform Engineers** - Deploy and operate bridge daemons on agent host machines.
- **Security Engineers** - Configure and audit the zero-trust PKI infrastructure.

---

## 6. Architecture

### 6.1 Components

```
┌─────────────────────┐        ┌────────────────────────────────┐
│  prd-manager-       │        │  Web / Next.js App             │
│  control-plane      │        │  (React + useBridgeSession)    │
│  (HTTP API)         │        └───────────┬────────────────────┘
└────────┬────────────┘                    │ WebSocket (JSON protocol)
         │ Go SDK (bridgeclient)           ▼
         │ gRPC + mTLS + JWT  ┌────────────────────────────────┐
         │                    │  bridge-client-node            │
         │                    │  (Node.js WebSocket adapter)   │
         │                    │  or Go HTTP + bridgelib/       │
         │                    │  WSHandler                     │
         │                    └───────────┬────────────────────┘
         │                                │ gRPC (plain or mTLS+JWT)
         ▼                                ▼
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

### 6.2 Operational Modes

The bridge ships two distinct runtime binaries with different assumptions, startup behaviours, and intended operators. Understanding this separation is fundamental to deploying and integrating the system correctly.

#### Local Server (`bridgectl server start`)

The local server is a developer-facing tool for managing AI agent sessions on a machine where the agent CLIs are already installed and configured through their **native interfaces** (e.g. `claude` authenticated via `claude auth login`, `codex` with `OPENAI_API_KEY` in the shell environment).

| Property | Behaviour |
|---|---|
| Binary | `cmd/bridgectl` |
| Startup validation | Binary existence and executability only — no API key checks |
| Credential source | Inherits the operator's existing shell environment |
| Transport security | Optional — defaults to plain gRPC on localhost |
| Intended operator | Developer or local user who has already configured their AI agents natively |
| Typical use | Local development, ad-hoc sessions, testing bridge plumbing without production infrastructure |

**Key assumption**: the developer has already authenticated each provider CLI using that provider's own tooling. The bridge does not manage or validate credentials; it simply launches the CLIs that are already ready to run.

**What the local server manages**: session lifecycle (start, stop, event streaming, reconnect), provider multiplexing, and the gRPC API surface — not credential provisioning.

#### Daemon (`bridge` / `cmd/bridge`)

The daemon is a production-grade service designed to run headlessly on a server or agent host. External systems (prd-manager-control-plane, ndara-ai-orchestrator, web clients) connect to it remotely over mTLS + JWT. It is expected to operate without user intervention after initial provisioning.

| Property | Behaviour |
|---|---|
| Binary | `cmd/bridge` |
| Startup validation | Per-provider: providers with all required credentials become available; providers with missing credentials register as unavailable (daemon does not exit) |
| Credential source | `/etc/ai-agent-bridge/agents.env` injected at service startup (e.g. via systemd drop-in), or inherited environment |
| Transport security | Strongly recommended — mTLS + JWT enforced when TLS certs and JWT keys are configured; falls back to plain gRPC with a warning when unconfigured (dev/local use only) |
| Intended operator | DevOps / platform engineer provisioning a persistent agent host |
| Typical use | Production deployments, headless CI/CD agents, integration target for control-plane services |

**Provider availability at startup**: the daemon performs per-provider validation at startup. A provider with all required credentials present is registered as available. A provider missing credentials (or whose startup probe fails) is registered as unavailable with a reason — the daemon continues serving and reports the unavailable provider through the `Health` RPC. Credentials can be added to the environment and the daemon restarted to make a previously unavailable provider available.

**Mixed-credential deployments**: a single daemon instance may have some providers fully credentialed and others not yet provisioned. This supports gradual rollout (e.g. start with Claude only, add Codex later) and mixed local/remote use cases where the same host runs both natively configured CLIs and daemon-managed sessions. The `Health` endpoint always reflects the current per-provider state.

**What the daemon manages**: everything the local server manages, plus zero-trust PKI, rate limiting, audit logging, systemd lifecycle, and the expectation of continuous uptime.

#### Mode Comparison

```
               Local Server (bridgectl)          Daemon (bridge)
               ─────────────────────────         ─────────────────────────
Operator       Developer                         DevOps / platform team
Setup          Native CLI auth already done      API keys in agents.env
Key check      At session launch (by the CLI)    Per-provider at startup; missing
                                                 keys → unavailable, not fatal
Transport      Plain gRPC (localhost default)    mTLS + JWT (required)
Auth           None (localhost only)             Per-project CAs + short-lived JWTs
Deployment     Foreground process / dev tool     systemd service
External conn  Not intended                      Primary purpose
Mixed creds    N/A                               Supported — partial key sets are
                                                 valid; only credentialed providers
                                                 accept sessions
```

#### Mixed-Mode on a Single Host

Both binaries may run on the same machine. A common pattern is:

- The **local server** (`bridgectl`) serves developer sessions using the developer's own native CLI credentials, bound to localhost.
- The **daemon** serves production sessions on a different port using credentials provisioned in `agents.env`, secured with mTLS + JWT.

There is no coordination between the two processes; they are independent and manage separate session sets.

---

### 6.3 Deliverables

| Deliverable | Description |
|---|---|
| `cmd/bridge` | Standalone bridge daemon binary |
| `cmd/bridge-ca` | CA and certificate management CLI |
| `pkg/bridgeclient` | Go SDK for consumer integration (gRPC client) |
| `pkg/bridgelib` | Embeddable Go library (no separate gRPC server; includes WebSocket handler) |
| `packages/bridge-client-node` | Node.js gRPC→WebSocket adapter + React hook (`useBridgeSession`) |
| Debian/Ubuntu package distribution | Signed `.deb` packages, apt repository metadata, install helper, and Ubuntu install docs |
| `proto/bridge/v1` | Protobuf service definitions |
| `internal/bridge` | Session supervisor, provider adapters, event buffer |
| `internal/auth` | mTLS, JWT, and zero-trust security primitives |
| `internal/pki` | CA management, cross-signing, cert generation |
| `docs/go-websocket-integration.md` | Guide for wiring the WebSocket protocol in a Go HTTP server |

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
ai-agent-bridge-ca (root)
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
- Certificate rotation: certs expire after 90 days; automated renewal via `ai-agent-bridge-ca renew`.
- Revocation: CRL distribution point served by bridge daemon.

### 7.5 Defense in Depth

- Bridge binds to configurable interface (default: private/localhost).
- Rate limiting on session creation and command input.
- Input size limits (max command text size).
- Secret redaction in event streams and logs.
- Audit logging of all authentication and authorization decisions.
- No plaintext traffic under any configuration.

---

## 7.6 Debian/Ubuntu Distribution

The bridge must be installable on supported Ubuntu hosts through a signed apt repository so operators can install and upgrade it with standard package-management workflows instead of building from source.

### Packaging Scope

- Ship a Debian package named `ai-agent-bridge`.
- Initial supported targets:
  - Ubuntu `24.04` (`noble`) on `amd64`
  - Ubuntu `25.04` (`plucky`) on `amd64`
- Install package contents to conventional system locations:
  - `ai-agent-bridge` and `ai-agent-bridge-ca` binaries in `/usr/bin`
  - default config in `/etc/ai-agent-bridge/bridge.yaml`
  - systemd unit in `/lib/systemd/system/ai-agent-bridge.service`
- Provide a default packaged config that allows the daemon to start on a fresh host without bundled provider CLIs or API keys.
- Provider CLIs and their API credentials remain operator-managed prerequisites and must be documented separately from the package install flow.

### Publishing and Hosting

- Release automation must build `.deb` artifacts from release tags using the existing release workflow pattern.
- The apt repository must be published in a GitHub-hosted location with the full Debian repository structure (`dists/`, `pool/`, `Packages`, `Release`, `InRelease`).
- Repository metadata and packages must be signed with a GPG key stored in GitHub Actions secrets.
- Release artifacts must also be attached to the GitHub release for direct inspection/download.

### Installation Flow

- Provide an `install.sh` helper that:
  - installs the repository signing key into `/etc/apt/keyrings`
  - writes the apt source list entry
  - runs `apt-get update`
  - installs `ai-agent-bridge`
- Installation documentation must include both the helper-script path and the equivalent manual apt commands.

### Acceptance Criteria

- A release tag builds a signed `.deb` for each supported Ubuntu target.
- The published apt repository is consumable with standard `apt` commands on supported Ubuntu releases.
- A clean Ubuntu host can install `ai-agent-bridge`, start the systemd service, and pass a basic daemon health check.
- The release workflow includes smoke coverage that validates apt installation in containers and on an EC2 host.
- `README.md` and `docs/` describe package installation, runtime prerequisites, and service behavior accurately.

## 7.7 External Deployment Profiles

The bridge package is deployment-neutral. Consumer platforms are responsible for authoring their own systemd drop-ins, credential injection, provisioning guides, and readiness checks. The package ships `bridge-example.yaml` as a reference config; all deployment-specific orchestration lives outside this repository.

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
    error: string        // reason if unavailable: missing credential, binary not found, probe failure, etc.
```

Provider availability reflects binary presence, credential availability, and daemon-startup probe results:

| Condition | `available` | `error` |
|---|---|---|
| Binary found, all required env vars set | `true` | `""` |
| Binary found, required env var missing | `false` | `"required env var CLAUDE_CODE_OAUTH_TOKEN not set"` |
| Binary not found or not executable | `false` | `"binary not found: claude"` |
| Startup probe timed out or failed (daemon only) | `false` | `"startup probe failed: ..."` |

Startup probe failures are recorded on the provider at daemon boot via `SetUnavailable` and returned by every subsequent `Health()` call until the daemon restarts. The local server (`bridgectl`) does not run startup probes, so this condition applies to the daemon only.

The daemon `status` field reflects overall daemon health, not aggregate provider health. A daemon with zero available providers still returns `"serving"` — callers must inspect per-provider entries to determine which providers can accept sessions.

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

## 12. CLI Tool: `ai-agent-bridge-ca`

### 12.1 Commands

```bash
# Initialize a new CA for this project
ai-agent-bridge-ca init --name "ai-agent-bridge" --out certs/

# Generate server certificate for the bridge daemon
ai-agent-bridge-ca issue --type server --cn "bridge.example.com" \
    --san "bridge.example.com,192.168.1.10" \
    --ca certs/ca.crt --ca-key certs/ca.key \
    --out certs/bridge

# Generate client certificate for a consumer project
ai-agent-bridge-ca issue --type client --cn "prd-manager" \
    --ca certs/ca.crt --ca-key certs/ca.key \
    --out certs/prd-manager-client

# Cross-sign another project's CA
ai-agent-bridge-ca cross-sign \
    --signer-ca certs/ca.crt --signer-key certs/ca.key \
    --target-ca ../prd-manager-control-plane/certs/ca.crt \
    --out certs/prd-manager-ca-cross-signed.crt

# Build a trust bundle (own CA + all cross-signed CAs)
ai-agent-bridge-ca bundle \
    --ca certs/ca.crt \
    --cross-signed certs/prd-manager-ca-cross-signed.crt \
    --cross-signed certs/ndara-ca-cross-signed.crt \
    --out certs/ca-bundle.crt

# Generate Ed25519 keypair for JWT signing
ai-agent-bridge-ca jwt-keygen --out certs/jwt-signing

# Renew expiring certificates
ai-agent-bridge-ca renew --cert certs/bridge.crt --ca certs/ca.crt --ca-key certs/ca.key

# Verify a certificate chain
ai-agent-bridge-ca verify --cert certs/bridge.crt --bundle certs/ca-bundle.crt
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
