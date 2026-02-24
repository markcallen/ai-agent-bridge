# AI Agent Bridge - MVP Implementation Record

This document records what was designed and built for the MVP. All items are complete.
For remaining and future work, see [PLAN.md](PLAN.md).

---

## Project Structure (as built)

```
ai-agent-bridge/
├── cmd/
│   ├── bridge/                  # Bridge daemon binary
│   │   └── main.go
│   └── bridge-ca/               # CA/cert management CLI
│       └── main.go
├── proto/
│   └── bridge/
│       └── v1/
│           ├── bridge.proto      # Service + message definitions
│           └── gen.go            # go:generate directive
├── gen/
│   └── bridge/
│       └── v1/                  # Generated protobuf Go code
├── pkg/
│   └── bridgeclient/            # Public Go SDK
│       ├── client.go
│       ├── options.go
│       ├── session.go
│       ├── events.go
│       ├── auth.go
│       ├── errors.go
│       ├── retry.go             # Retry policy implementation
│       ├── cursor_store.go      # Client-side subscriber cursor tracking
│       └── example_test.go
├── internal/
│   ├── auth/                    # mTLS + JWT primitives
│   │   ├── mtls.go
│   │   ├── jwt.go
│   │   ├── jwt_test.go
│   │   ├── interceptors.go      # gRPC auth interceptors
│   │   ├── audit.go             # Audit logging helpers
│   │   └── peer.go              # Peer cert extraction
│   ├── pki/                     # CA management, cross-signing
│   │   ├── ca.go
│   │   ├── issue.go
│   │   ├── crosssign.go
│   │   ├── bundle.go
│   │   ├── jwtkey.go
│   │   ├── verify.go
│   │   └── ca_test.go
│   ├── bridge/                  # Core bridge logic
│   │   ├── supervisor.go        # Session lifecycle manager
│   │   ├── supervisor_test.go
│   │   ├── provider.go          # Provider interface
│   │   ├── registry.go          # Provider registry
│   │   ├── eventbuf.go          # Ring buffer per session
│   │   ├── eventbuf_test.go
│   │   ├── subscribermgr.go     # Per-subscriber cursor manager
│   │   ├── subscribermgr_test.go
│   │   ├── policy.go            # Limits, path validation
│   │   └── errors.go            # Bridge-specific error types
│   ├── provider/                # Provider adapters
│   │   ├── stdio.go             # Shared stdio subprocess logic
│   │   ├── stdio_test.go
│   │   ├── codex.go
│   │   ├── claude.go
│   │   └── opencode.go
│   ├── redact/                  # Secret redaction
│   │   ├── redactor.go
│   │   └── redactor_test.go
│   ├── config/                  # Configuration loading
│   │   ├── config.go
│   │   ├── config_test.go
│   │   ├── env.go               # Environment variable / .env support
│   │   └── env_test.go
│   └── server/                  # gRPC server implementation
│       ├── server.go
│       ├── server_test.go
│       ├── ratelimit.go         # Token bucket rate limiting
│       └── validate.go          # Request field validation
├── e2e/
│   └── cmd/e2e-test/
│       └── main.go              # Dockerized end-to-end test runner
├── examples/
│   ├── chat/                    # Interactive chat example
│   │   ├── main.go
│   │   └── main_e2e_test.go
│   └── runprompt/               # Single-prompt example
│       └── main.go
├── certs/                       # Generated certs (gitignored)
├── config/
│   └── bridge.yaml              # Default configuration
├── scripts/
│   └── dev_certs.sh             # Dev certificate generation
├── docker-compose.yaml          # Integration test environment
├── Makefile
├── go.mod
├── go.sum
├── .env.example
├── PRD.md
├── ARCHITECTURE.md
└── .gitignore
```

---

## Phase 1: Foundation

**Goal**: Buildable bridge daemon with a single provider, gRPC API, and basic auth.

### 1.1 Project Scaffolding
- [x] Initialize Go module (`github.com/markcallen/ai-agent-bridge`)
- [x] Set up `.gitignore` (certs/, gen/, vendor/)
- [x] Create directory structure
- [x] Add Makefile with targets: `build`, `proto`, `test`, `lint`, `certs`, `dev-setup`, `test-e2e`

### 1.2 Protobuf Definitions
- [x] Write `proto/bridge/v1/bridge.proto` with full service definition
  - `BridgeService` with all RPCs defined in PRD section 8
  - Message types: session lifecycle, input, events, health, providers
  - `SessionStatus` enum, `EventType` enum
- [x] Add `buf.gen.yaml` / `go:generate` for protoc codegen
- [x] Generate Go stubs into `gen/bridge/v1/`

### 1.3 PKI / Certificate Authority
- [x] `internal/pki/ca.go` - CA initialization (RSA 4096 root key + self-signed cert)
- [x] `internal/pki/issue.go` - Issue server/client certs signed by CA
- [x] `internal/pki/crosssign.go` - Cross-sign external CA certificates
- [x] `internal/pki/bundle.go` - Build trust bundles (own CA + cross-signed CAs)
- [x] `internal/pki/jwtkey.go` - Ed25519 keypair generation
- [x] `internal/pki/verify.go` - Certificate chain verification
- [x] `cmd/bridge-ca/main.go` - CLI wrapping PKI functions
  - Subcommands: `init`, `issue`, `cross-sign`, `bundle`, `jwt-keygen`, `verify`
- [x] Unit tests for CA operations

### 1.4 Auth Layer
- [x] `internal/auth/mtls.go` - Server and client TLS configs (mTLS required)
  - `ServerTLSConfig(caBundlePath, certPath, keyPath)` - requires client certs
  - `ClientTLSConfig(caBundlePath, certPath, keyPath, serverName)` - presents client cert
  - Minimum TLS 1.3
- [x] `internal/auth/jwt.go` - Ed25519 JWT signing and verification
  - `JWTVerifier` - verifies tokens with multiple public keys (one per issuer)
  - `JWTIssuer` - mints tokens (used by SDK and bridge-ca)
  - Claims: `sub`, `project_id`, `aud`, `iat`, `exp`
  - Max TTL enforcement (reject tokens with TTL > configured max)
- [x] `internal/auth/interceptors.go` - gRPC interceptors for JWT extraction and verification
  - Unary + stream JWT verification
  - Health endpoint exempted from auth
  - Audit logging interceptor
- [x] `internal/auth/peer.go` - Extract peer certificate CN from gRPC context
- [x] `internal/auth/audit.go` - Structured audit log helpers
- [x] Unit tests for JWT mint/verify round-trip

### 1.5 Provider Framework
- [x] `internal/bridge/provider.go` - `Provider` interface definition
- [x] `internal/bridge/registry.go` - Provider registry (register by name, lookup, health)
- [x] `internal/provider/stdio.go` - Shared stdio subprocess adapter
  - Spawn process with `exec.CommandContext`
  - Set working directory to `repo_path`
  - Pipe stdin/stdout/stderr
  - Monitor process health (PID check)
  - Graceful shutdown: SIGTERM → wait grace period → SIGKILL
  - Emit structured events from stdout/stderr lines
  - Provider startup timeout enforcement
- [x] `internal/provider/codex.go` - Codex-specific adapter (binary name, default args)
- [x] Unit tests with mock process (`stdio_test.go`)

### 1.6 Session Supervisor
- [x] `internal/bridge/supervisor.go` - Session lifecycle manager
  - `Start(cfg SessionConfig)` → spawn via provider adapter
  - `Stop(sessionID, force)` → graceful/force stop
  - `Send(sessionID, text)` → write to process stdin
  - `Get(sessionID)` → return session status
  - `List(projectID)` → list sessions for project
  - Concurrent session tracking with `sync.RWMutex`
  - Crash detection: goroutine watching `cmd.Wait()`, emit `SESSION_FAILED`
- [x] `internal/bridge/policy.go` - Policy enforcement
  - Per-project max sessions
  - Global max sessions
  - `repo_path` allowlist validation (glob patterns)
  - Input size limits
- [x] `internal/bridge/eventbuf.go` - Per-session ring buffer
  - Fixed capacity, monotonic sequence numbers
  - `Append(event)` → assigns seq, drops oldest if full
  - `After(seq)` → returns events after given sequence
  - `Subscribe()` → returns channel for live events
  - Overflow detection and `BUFFER_OVERFLOW` event emission
- [x] Unit tests for supervisor, policy, and event buffer

### 1.7 Session Pub/Sub Queueing
- [x] `internal/bridge/subscribermgr.go` - Per-session subscriber cursor manager
  - Track subscribers by `(project_id, session_id, subscriber_id)`
  - Track `ack_seq` and deliver `seq > ack_seq` on reconnect
  - Preserve strict in-order delivery guarantees per subscriber
  - Keep enqueueing provider events while subscribers are disconnected
  - Emit `BUFFER_OVERFLOW` when retention is exceeded
- [x] Extend supervisor/server integration to route events through subscriber manager
- [x] Configuration for subscriber limits and TTL cleanup
- [x] Unit tests: reconnect replay, ack progression, overflow behavior, multi-subscriber fanout

### 1.8 gRPC Server
- [x] `internal/server/server.go` - Implement `BridgeService`
  - Wire supervisor, registry, and event buffer
  - `StartSession` → validate JWT claims, check policy, call supervisor
  - `StopSession` → validate JWT claims, call supervisor
  - `GetSession` / `ListSessions` → read from supervisor
  - `SendInput` → validate JWT claims, call supervisor
  - `StreamEvents` → replay from buffer, then stream live
  - `Health` → aggregate provider health
  - `ListProviders` → enumerate registered providers
- [x] Project-scoped authorization (JWT `project_id` must match request) on all session-scoped RPCs
- [x] `internal/server/ratelimit.go` - Token bucket rate limiting
  - Per-client rate limiting on `StartSession`
  - Per-session rate limiting on `SendInput`
  - Global RPC rate limiting
  - Return `RESOURCE_EXHAUSTED` on limit hit
- [x] `internal/server/validate.go` - Request field validation
  - UUID format validation for `session_id`
  - Control character validation on all string fields
  - `repo_path` absolute path and allowlist validation
  - Provider name validation against registry
- [x] `internal/server/server_test.go` - Unit tests

### 1.9 Secret Redaction
- [x] `internal/redact/redactor.go` - Configurable regex-based redactor
- [x] Apply redaction to all event text before buffering
- [x] Apply redaction to all log output
- [x] Unit tests for redaction patterns

### 1.10 Configuration
- [x] `internal/config/config.go` - YAML config loading
  - Struct matching `bridge.yaml` schema from PRD section 13
  - Defaults for all fields
- [x] `internal/config/env.go` - Environment variable and `.env` file support
- [x] `config/bridge.yaml` - Default config file
- [x] `.env.example` - Environment variable template
- [x] Unit tests for config loading and env parsing

### 1.11 Bridge Daemon Entry Point
- [x] `cmd/bridge/main.go` - Main daemon binary
  - Load config from file (flag: `--config`)
  - Initialize PKI/mTLS
  - Register providers from config
  - Start gRPC server with mTLS + JWT interceptors
  - Graceful shutdown on SIGINT/SIGTERM
  - Health/ready logging on startup
- [x] `scripts/dev_certs.sh` - Generate dev certs using `bridge-ca`

---

## Phase 2: Full Provider Support

**Goal**: Claude and OpenCode adapters, provider health checks.

### 2.1 Claude Adapter
- [x] `internal/provider/claude.go` - Claude CLI adapter
  - Binary: `claude`
  - Default args: `["--print", "--verbose"]`
  - AGENT_READY / RESPONSE_COMPLETE signal handling

### 2.2 OpenCode Adapter
- [x] `internal/provider/opencode.go` - OpenCode adapter
  - Binary: `opencode`

### 2.3 Provider Health Checks
- [x] Each provider implements `Health(ctx)` → checks binary exists and is executable
- [x] `ListProviders` RPC returns availability status
- [x] `StartSession` returns typed error if provider is unavailable
- [x] Provider startup timeout enforcement (configurable per provider)

---

## Phase 3: Go SDK (`bridgeclient`)

**Goal**: Go client library for consumer integration.

### 3.1 Client Core
- [x] `pkg/bridgeclient/client.go` - Main client type with gRPC dial + mTLS credentials, `Close()`
- [x] `pkg/bridgeclient/options.go` - Functional options
  - `WithTarget(addr)`, `WithMTLS(cfg)`, `WithJWT(cfg)`, `WithTimeout(d)`, `WithRetry(cfg)`
- [x] `pkg/bridgeclient/retry.go` - Configurable retry policy with exponential backoff
- [x] `pkg/bridgeclient/auth.go` - Credential management
  - Load mTLS certs → `tls.Config`
  - Load Ed25519 private key → auto-mint JWTs
  - Per-call credential injection via gRPC `PerRPCCredentials`
  - Auto-renewal: mint new JWT before current one expires

### 3.2 Session Operations
- [x] `pkg/bridgeclient/session.go` - Typed wrappers for all session RPCs

### 3.3 Event Streaming
- [x] `pkg/bridgeclient/events.go`
  - `StreamEvents(ctx, req)` → typed event stream
  - Automatic reconnect with `after_seq` resume
  - Backoff on reconnect failures
  - Context cancellation support
  - Stable `subscriber_id` per consumer/session stream
  - Server-side cursor tracking with `ack_seq` per subscriber
  - Reconnect replays unseen queued events first, then live tail
- [x] `pkg/bridgeclient/cursor_store.go` - In-process subscriber cursor tracking

### 3.4 Error Handling
- [x] `pkg/bridgeclient/errors.go` - Typed errors with gRPC status code mapping
  - `ErrSessionNotFound`, `ErrSessionAlreadyExists`, `ErrProviderUnavailable`
  - `ErrUnauthorized`, `ErrPermissionDenied`, `ErrInputTooLarge`, `ErrSessionLimitReached`

### 3.5 Documentation
- [x] Example code in `pkg/bridgeclient/example_test.go`

---

## Phase 4: Dev Environment & Integration Testing

**Goal**: One-command dev setup and a working end-to-end test harness.

- [x] `scripts/dev_certs.sh` - Generate all dev certs for local testing
- [x] `docker-compose.yaml` - Run bridge + mock agents for integration testing
- [x] `Makefile` target: `make dev-setup` - one-command dev environment
- [x] `Makefile` target: `make test-e2e` - dockerized end-to-end validation
- [x] `e2e/cmd/e2e-test/main.go` - E2E test runner
- [x] `examples/chat/` and `examples/runprompt/` - Working usage examples

---

## Phase 5: Unit & Integration Tests

**Goal**: Core test coverage for all MVP components.

### 5.1 Unit Tests (all complete)
- [x] Session start/stop state transitions
- [x] Input routing and write failures
- [x] Event sequencing and buffer overflow
- [x] Policy enforcement (limits, path validation)
- [x] Provider registry and adapter selection
- [x] Provider startup timeout behavior
- [x] JWT mint/verify with Ed25519
- [x] mTLS config generation and validation
- [x] CA operations (init, issue, cross-sign, bundle)
- [x] gRPC server authorization and error mapping
- [x] Secret redaction patterns
- [x] Config loading and validation
- [x] Subscriber reconnect replay, ack progression, overflow, multi-subscriber fanout

### 5.2 Integration Tests (complete)
- [x] End-to-end: start → input → output → stop (with real process)
- [x] mTLS rejection (bad cert, expired cert, wrong CA)
- [x] JWT rejection (expired, wrong audience, wrong issuer)
- [x] Provider unavailability handling
- [x] Invalid input handling (oversized, malformed)

---

## Dependency Summary

### Direct Dependencies
- `google.golang.org/grpc` - gRPC framework
- `google.golang.org/protobuf` - Protobuf runtime
- `github.com/golang-jwt/jwt/v5` - JWT library
- `golang.org/x/crypto` - Ed25519 support
- `gopkg.in/yaml.v3` - Configuration parsing

### Dev/Build Dependencies
- `buf` or `protoc` + `protoc-gen-go` + `protoc-gen-go-grpc` - Protobuf code generation
- `golangci-lint` - Linting
