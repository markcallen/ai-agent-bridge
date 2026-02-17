# AI Agent Bridge - Architecture

## Overview

The AI Agent Bridge is a standalone gRPC daemon and Go SDK that provides a secure, zero-trust communication layer between control-plane systems and AI agent processes. It manages AI agent subprocess lifecycles (codex, claude, opencode) and exposes a unified API for session management, command routing, and event streaming.

## System Context

```
┌──────────────────────────────────────────────────────────────────────┐
│                         User Interfaces                              │
│   Slack    Discord    CLI (WebSocket)    Web UI (future)             │
└─────┬────────┬──────────┬───────────────────────────────────────────┘
      │        │          │
      ▼        ▼          ▼
┌──────────────────────────────────┐    ┌──────────────────────────────┐
│   prd-manager-control-plane      │    │   ndara-ai-orchestrator      │
│                                  │    │                              │
│   Go HTTP API + SQLite           │    │   Go gRPC + mTLS + JWT       │
│   - Project/session management   │    │   - Multi-machine dispatch   │
│   - Slack/CLI fan-out            │    │   - Agent registry           │
│   - PRD workflows                │    │   - Chat (Slack/Discord)     │
│                                  │    │   - OpenCode-based planner   │
│   Uses: bridgeclient (Go SDK)   │    │   Uses: bridgeclient (Go SDK)│
└──────────────┬───────────────────┘    └──────────────┬───────────────┘
               │                                       │
               │  gRPC + mTLS + JWT                    │  gRPC + mTLS + JWT
               │  (per-project CA                      │  (per-project CA
               │   cross-signing)                      │   cross-signing)
               ▼                                       ▼
┌──────────────────────────────────────────────────────────────────────┐
│                      AI Agent Bridge Daemon                          │
│                      (cmd/bridge)                                    │
│                                                                      │
│  ┌────────────┐  ┌────────────────┐  ┌───────────────────────────┐  │
│  │ gRPC Server│  │ JWT Verifier   │  │ mTLS (TLS 1.3)           │  │
│  │            │  │ (multi-issuer) │  │ (RequireAndVerifyClient)  │  │
│  └─────┬──────┘  └────────────────┘  └───────────────────────────┘  │
│        │                                                             │
│  ┌─────▼──────────────────────────────────────────────────────────┐  │
│  │                    Session Supervisor                           │  │
│  │  - Start/Stop/Send/Get/List                                    │  │
│  │  - Policy enforcement (limits, path validation)                │  │
│  │  - Event forwarding to per-session ring buffers                │  │
│  └─────┬──────────────────────────────────────────────────────────┘  │
│        │                                                             │
│  ┌─────▼──────────────────────────────────────────────────────────┐  │
│  │                    Provider Registry                           │  │
│  │  ┌─────────┐  ┌─────────┐  ┌──────────┐                      │  │
│  │  │ codex   │  │ claude  │  │ opencode │    (all stdio-based)  │  │
│  │  │ adapter │  │ adapter │  │ adapter  │                       │  │
│  │  └────┬────┘  └────┬────┘  └────┬─────┘                      │  │
│  └───────┼────────────┼────────────┼─────────────────────────────┘  │
│          │            │            │                                  │
│    ┌─────▼────┐ ┌─────▼────┐ ┌────▼─────┐                          │
│    │ codex    │ │ claude   │ │ opencode │   (child processes)       │
│    │ process  │ │ process  │ │ process  │                           │
│    │ (stdio)  │ │ (stdio)  │ │ (stdio)  │                           │
│    └──────────┘ └──────────┘ └──────────┘                           │
└──────────────────────────────────────────────────────────────────────┘
```

## Component Architecture

### cmd/bridge (Daemon)

Entry point for the bridge daemon process. Responsibilities:
- Load YAML configuration
- Register provider adapters from config
- Initialize mTLS + JWT auth (optional for dev mode)
- Start gRPC server
- Graceful shutdown on SIGINT/SIGTERM

### cmd/bridge-ca (CLI Tool)

Certificate Authority management tool. Subcommands:
- `init` - Generate a new ECDSA P-384 CA
- `issue` - Issue server or client certificates
- `cross-sign` - Cross-sign external project CAs
- `bundle` - Build trust bundles
- `jwt-keygen` - Generate Ed25519 JWT signing keypairs
- `verify` - Verify certificate chains

### internal/server (gRPC Service)

Implements `bridge.v1.BridgeService`:

| RPC | Description |
|-----|-------------|
| `StartSession` | Create and start agent process via provider |
| `StopSession` | Graceful/force stop agent process |
| `GetSession` | Query session metadata and state |
| `ListSessions` | List sessions (optionally by project) |
| `SendInput` | Write text to agent stdin |
| `StreamEvents` | Replay + live stream session events |
| `Health` | Provider availability check |
| `ListProviders` | Enumerate registered providers |

### internal/bridge (Core Logic)

**Supervisor** (`supervisor.go`)
- Manages session lifecycle state machine
- Delegates to provider adapters for process management
- Enforces policy (session limits, path validation, input size)
- Forwards provider events to per-session ring buffers

**EventBuffer** (`eventbuf.go`)
- Bounded ring buffer per session
- Monotonic sequence numbers for ordering
- Subscribe/unsubscribe for live streaming
- `After(seq)` for replay from any point

**Provider Interface** (`provider.go`)
- `Start(ctx, config) → SessionHandle`
- `Stop(handle)`
- `Send(handle, text)`
- `Events(handle) → <-chan Event`
- `Health(ctx) → error`

**Registry** (`registry.go`)
- Register providers by ID
- Lookup and health-check all providers

**Policy** (`policy.go`)
- Per-project and global session limits
- Repo path allowlist (glob patterns)
- Input size limits

### internal/provider (Adapters)

**StdioProvider** (`stdio.go`)
- Shared subprocess adapter used by all providers
- Spawns process with `exec.CommandContext`
- Pipes stdin/stdout/stderr
- Graceful shutdown: SIGTERM → grace period → SIGKILL
- Environment filtering (strips sensitive variables)
- Buffered event channel with line-based parsing

Provider-specific adapters set binary name and default args:
- `codex.go` → `codex --quiet`
- `claude.go` → `claude --print --verbose`
- `opencode.go` → `opencode`

### internal/auth (Security)

**mTLS** (`mtls.go`)
- TLS 1.3 minimum
- Server: `RequireAndVerifyClientCert`
- Client: presents cert, verifies server against CA bundle
- No `InsecureSkipVerify`

**JWT** (`jwt.go`)
- Ed25519 (EdDSA) signing — not HS256 shared secrets
- Multi-issuer verifier (one public key per consumer project)
- Max TTL enforcement (reject overly long-lived tokens)
- Claims: `sub`, `project_id`, `aud`, `iss`, `iat`, `exp`

**Interceptors** (`interceptors.go`)
- Unary + stream gRPC interceptors
- Extract Bearer token from `authorization` metadata
- Verify and inject claims into context
- Health endpoint exempted from auth

### internal/pki (Certificate Management)

- ECDSA P-384 CA generation
- Server/client certificate issuance (90-day validity)
- Cross-signing for multi-project trust
- Trust bundle assembly
- Ed25519 JWT keypair generation

### pkg/bridgeclient (Go SDK)

Public API for consumer integration:
- `New(opts...)` → `*Client`
- Session operations: Start, Stop, Get, List, SendInput
- Event streaming with automatic reconnect + backoff
- mTLS + auto-renewing JWT credentials
- Typed errors mapped from gRPC status codes

## Security Architecture

### Zero-Trust Model

```
┌─────────────┐         ┌─────────────┐         ┌─────────────┐
│ prd-manager │         │   bridge    │         │   ndara     │
│     CA      │         │     CA      │         │     CA      │
└──────┬──────┘         └──────┬──────┘         └──────┬──────┘
       │                       │                       │
       │  cross-sign ◄────────►│◄────────► cross-sign  │
       │                       │                       │
       ▼                       ▼                       ▼
┌──────────────┐        ┌──────────────┐        ┌──────────────┐
│ prd-manager  │  mTLS  │   bridge     │  mTLS  │   ndara      │
│ client cert  │───────►│ server cert  │◄───────│ client cert  │
└──────────────┘  JWT   └──────────────┘  JWT   └──────────────┘
```

### Authentication Flow

1. **TLS Handshake**: Client presents certificate signed by its project CA. Bridge verifies against cross-signed CA trust bundle.
2. **JWT Verification**: Client sends `authorization: Bearer <token>` in gRPC metadata. Bridge verifies Ed25519 signature, audience, issuer, expiry, and max TTL.
3. **Authorization**: Bridge checks JWT `project_id` claim matches the requested session's project.

### Certificate Setup (3-project example)

```bash
# 1. Each project initializes its own CA
bridge-ca init --name prd-manager --out prd-manager/certs/
bridge-ca init --name ai-agent-bridge --out bridge/certs/
bridge-ca init --name ndara --out ndara/certs/

# 2. Bridge cross-signs consumer CAs
bridge-ca cross-sign \
  --signer-ca bridge/certs/ca.crt --signer-key bridge/certs/ca.key \
  --target-ca prd-manager/certs/ca.crt \
  --out bridge/certs/prd-manager-cross.crt

bridge-ca cross-sign \
  --signer-ca bridge/certs/ca.crt --signer-key bridge/certs/ca.key \
  --target-ca ndara/certs/ca.crt \
  --out bridge/certs/ndara-cross.crt

# 3. Build trust bundle
bridge-ca bundle --out bridge/certs/ca-bundle.crt \
  bridge/certs/ca.crt \
  bridge/certs/prd-manager-cross.crt \
  bridge/certs/ndara-cross.crt

# 4. Issue certs
bridge-ca issue --type server --cn bridge.local --san "bridge.local,127.0.0.1" \
  --ca bridge/certs/ca.crt --ca-key bridge/certs/ca.key --out bridge/certs/

bridge-ca issue --type client --cn prd-manager \
  --ca prd-manager/certs/ca.crt --ca-key prd-manager/certs/ca.key \
  --out prd-manager/certs/

# 5. Generate JWT keys
bridge-ca jwt-keygen --out prd-manager/certs/jwt-signing
```

## Data Flow

### Session Lifecycle

```
Consumer                    Bridge Daemon                Provider Process
   │                            │                            │
   │── StartSession ──────────►│                            │
   │                            │── exec.CommandContext ────►│
   │                            │◄── stdout/stderr (lines) ──│
   │◄── StartSessionResponse ──│                            │
   │                            │                            │
   │── SendInput ─────────────►│                            │
   │                            │── stdin.Write ────────────►│
   │◄── SendInputResponse ────│                            │
   │                            │◄── stdout (response) ─────│
   │                            │── EventBuffer.Append ──┐   │
   │                            │                        │   │
   │── StreamEvents ──────────►│                        │   │
   │◄── replay from buffer ────│◄───────────────────────┘   │
   │◄── live events ───────────│                            │
   │                            │                            │
   │── StopSession ───────────►│                            │
   │                            │── SIGTERM ────────────────►│
   │                            │   (grace period)           │
   │                            │── SIGKILL (if needed) ───►│
   │◄── StopSessionResponse ──│                            │
```

### Event Replay + Live Streaming

```
Client reconnects with after_seq=42:

EventBuffer: [seq:38, seq:39, seq:40, seq:41, seq:42, seq:43, seq:44]
                                                       ▲
                                              replay starts here

1. Replay: seq:43, seq:44 sent immediately
2. Subscribe to live channel
3. New events (seq:45, 46, ...) streamed as they arrive
4. Duplicate detection: skip any seq <= last sent
```

## Configuration

```yaml
server:
  listen: "0.0.0.0:9445"        # gRPC listen address

tls:
  ca_bundle: "certs/ca-bundle.crt"  # Trust bundle
  cert: "certs/bridge.crt"          # Server certificate
  key: "certs/bridge.key"           # Server private key

auth:
  jwt_public_keys:                  # One per consumer project
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

providers:
  codex:
    binary: "codex"
    args: ["--quiet"]
  claude:
    binary: "claude"
    args: ["--print", "--verbose"]
  opencode:
    binary: "opencode"
    args: []
```

## Integration Points

### prd-manager-control-plane

Replace `internal/agent/Manager` with `bridgeclient.Client`:

```go
// Before: in-process subprocess management
agentManager.Start(projectID, sessionID, repoPath)
agentManager.Send(projectID, sessionID, text)
agentManager.Stop(projectID, sessionID)

// After: bridge-backed remote management
bridgeClient.StartSession(ctx, &bridgev1.StartSessionRequest{
    ProjectId: projectID,
    SessionId: sessionID,
    RepoPath:  repoPath,
    Provider:  "codex",
})
bridgeClient.SendInput(ctx, &bridgev1.SendInputRequest{
    SessionId: sessionID,
    Text:      text,
})
bridgeClient.StopSession(ctx, &bridgev1.StopSessionRequest{
    SessionId: sessionID,
})
```

### ndara-ai-orchestrator

Wire `bridgeclient.Client` into `agentd` to replace stub handlers:

```go
// RunTask → bridge StartSession + SendInput
func (s *server) RunTask(ctx context.Context, req *orchv1.RunTaskRequest) {
    bridgeClient.StartSession(ctx, &bridgev1.StartSessionRequest{
        ProjectId: req.ProjectId,
        SessionId: req.RunId,
        RepoPath:  repoPathForRepo(req.RepoId),
        Provider:  "codex",
    })
    bridgeClient.SendInput(ctx, &bridgev1.SendInputRequest{
        SessionId: req.RunId,
        Text:      req.TaskText,
    })
}

// StreamEvents → bridge StreamEvents mapped to orch.v1.Event
```

## Directory Structure

```
ai-agent-bridge/
├── cmd/
│   ├── bridge/           # Daemon entry point
│   └── bridge-ca/        # CA management CLI
├── proto/bridge/v1/      # Protobuf service definitions
├── gen/bridge/v1/        # Generated Go stubs
├── pkg/bridgeclient/     # Public Go SDK
├── internal/
│   ├── auth/             # mTLS + JWT
│   ├── pki/              # CA management
│   ├── bridge/           # Supervisor, EventBuffer, Policy, Registry
│   ├── provider/         # Stdio adapter + provider implementations
│   ├── config/           # YAML config loading
│   └── server/           # gRPC service implementation
├── config/               # Default configuration
├── certs/                # Generated certs (gitignored)
└── scripts/              # Dev setup scripts
```
