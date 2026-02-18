# AI Agent Bridge - Implementation Plan

## Project Structure

```
ai-agent-bridge/
├── cmd/
│   ├── bridge/              # Bridge daemon binary
│   │   └── main.go
│   └── bridge-ca/           # CA/cert management CLI
│       └── main.go
├── proto/
│   └── bridge/
│       └── v1/
│           ├── bridge.proto  # Service + message definitions
│           └── gen.go        # go:generate directive
├── gen/
│   └── bridge/
│       └── v1/              # Generated protobuf Go code
├── pkg/
│   └── bridgeclient/        # Public Go SDK
│       ├── client.go
│       ├── options.go
│       ├── session.go
│       ├── events.go
│       ├── auth.go
│       └── errors.go
├── internal/
│   ├── auth/                # mTLS + JWT primitives
│   │   ├── mtls.go
│   │   ├── jwt.go
│   │   └── jwt_test.go
│   ├── pki/                 # CA management, cross-signing
│   │   ├── ca.go
│   │   ├── issue.go
│   │   ├── crosssign.go
│   │   ├── bundle.go
│   │   └── ca_test.go
│   ├── bridge/              # Core bridge logic
│   │   ├── supervisor.go    # Session lifecycle manager
│   │   ├── supervisor_test.go
│   │   ├── provider.go      # Provider interface
│   │   ├── registry.go      # Provider registry
│   │   ├── eventbuf.go      # Ring buffer per session
│   │   ├── eventbuf_test.go
│   │   └── policy.go        # Limits, path validation
│   ├── provider/            # Provider adapters
│   │   ├── stdio.go         # Shared stdio subprocess logic
│   │   ├── codex.go
│   │   ├── claude.go
│   │   ├── opencode.go
│   │   └── stdio_test.go
│   ├── config/              # Configuration loading
│   │   └── config.go
│   └── server/              # gRPC server implementation
│       ├── server.go
│       ├── server_test.go
│       └── interceptors.go
├── certs/                   # Generated certs (gitignored)
├── config/
│   └── bridge.yaml          # Default configuration
├── scripts/
│   └── dev_certs.sh         # Dev certificate generation
├── go.mod
├── go.sum
├── PRD.md
├── PLAN.md
└── .gitignore
```

---

## Phase 1: Foundation (MVP Core)

**Goal**: Buildable bridge daemon with a single provider, gRPC API, and basic auth.

### 1.1 Project Scaffolding
- [x] Initialize Go module (`github.com/markcallen/ai-agent-bridge`)
- [x] Set up `.gitignore` (certs/, gen/, vendor/)
- [x] Create directory structure
- [x] Add Makefile with targets: `build`, `proto`, `test`, `lint`, `certs`

### 1.2 Protobuf Definitions
- [x] Write `proto/bridge/v1/bridge.proto` with full service definition
  - `BridgeService` with all RPCs defined in PRD section 8
  - Message types: session lifecycle, input, events, health, providers
  - `SessionStatus` enum, `EventType` enum
- [x] Add `buf.gen.yaml` or `go:generate` for protoc codegen
- [x] Generate Go stubs into `gen/bridge/v1/`

### 1.3 PKI / Certificate Authority
- [x] `internal/pki/ca.go` - CA initialization (generate RSA 4096 root key + self-signed cert)
- [x] `internal/pki/issue.go` - Issue server/client certs signed by CA
- [x] `internal/pki/crosssign.go` - Cross-sign external CA certificates
- [x] `internal/pki/bundle.go` - Build trust bundles (own CA + cross-signed CAs)
- [x] `cmd/bridge-ca/main.go` - CLI wrapping PKI functions
  - Subcommands: `init`, `issue`, `cross-sign`, `bundle`, `jwt-keygen`, `verify`
  - Note: `renew` subcommand not yet implemented
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
- [x] gRPC interceptors for JWT extraction and verification
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
- [x] `internal/provider/codex.go` - Codex-specific adapter (binary name, default args)
- [x] Unit tests with mock process

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

### 1.7 gRPC Server
- [x] `internal/server/server.go` - Implement `BridgeService`
  - Wire supervisor, registry, and event buffer
  - `StartSession` → validate JWT claims, check policy, call supervisor
  - `StopSession` → validate JWT claims, call supervisor
  - `GetSession` / `ListSessions` → read from supervisor
  - `SendInput` → validate JWT claims, call supervisor
  - `StreamEvents` → replay from buffer, then stream live
  - `Health` → aggregate provider health
  - `ListProviders` → enumerate registered providers
- [x] `internal/auth/interceptors.go` - Auth interceptors (in auth package, not server)
  - Unary + stream JWT verification
  - Health endpoint exempted from auth
- [x] Project-scoped authorization (JWT `project_id` must match request) on all session-scoped RPCs
- [x] Audit logging interceptor
- [x] `internal/server/server_test.go` - Unit tests

### 1.8 Configuration
- [x] `internal/config/config.go` - YAML config loading
  - Struct matching `bridge.yaml` schema from PRD section 13
  - Defaults for all fields
- [x] `config/bridge.yaml` - Default config file

### 1.9 Bridge Daemon Entry Point
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
- [ ] Parse Claude-specific output format if applicable
- [ ] Integration test: start → input → output → stop

### 2.2 OpenCode Adapter
- [x] `internal/provider/opencode.go` - OpenCode adapter
  - Binary: `opencode`
- [ ] Integration test

### 2.3 Provider Health Checks
- [x] Each provider implements `Health(ctx)` → checks binary exists and is executable
- [x] `ListProviders` RPC returns availability status
- [x] `StartSession` returns typed error if provider is unavailable
- [x] Provider startup timeout enforcement (configurable per provider)

---

## Phase 3: Go SDK (`bridgeclient`)

**Goal**: Production-ready Go client library for consumer integration.

### 3.1 Client Core
- [x] `pkg/bridgeclient/client.go` - Main client type
  - gRPC dial with mTLS credentials
  - `Close()` for cleanup
- [x] `pkg/bridgeclient/options.go` - Functional options
  - `WithTarget(addr)` - bridge address
  - `WithMTLS(cfg)` - mTLS configuration
  - `WithJWT(cfg)` - JWT signing configuration
  - `WithTimeout(d)` - per-call timeout
- [ ] `WithRetry(cfg)` - retry policy (not yet implemented)
- [x] `pkg/bridgeclient/auth.go` - Credential management
  - Load mTLS certs → `tls.Config`
  - Load Ed25519 private key → auto-mint JWTs
  - Per-call credential injection via gRPC `PerRPCCredentials`
  - Auto-renewal: mint new JWT before current one expires

### 3.2 Session Operations
- [x] `pkg/bridgeclient/session.go` - Typed wrappers
  - `StartSession(ctx, req)` → response + error
  - `StopSession(ctx, req)` → response + error
  - `GetSession(ctx, req)` → response + error
  - `ListSessions(ctx, req)` → response + error
  - `SendInput(ctx, req)` → response + error

### 3.3 Event Streaming
- [x] `pkg/bridgeclient/events.go` - Event subscription
  - `StreamEvents(ctx, req)` → typed event stream
  - Automatic reconnect with `after_seq` resume
  - Backoff on reconnect failures
  - Context cancellation support

### 3.4 Error Handling
- [x] `pkg/bridgeclient/errors.go` - Typed errors
  - `ErrSessionNotFound`
  - `ErrSessionAlreadyExists`
  - `ErrProviderUnavailable`
  - `ErrUnauthorized`
  - `ErrPermissionDenied`
  - `ErrInputTooLarge`
  - `ErrSessionLimitReached`
  - gRPC status code mapping

### 3.5 Documentation
- [ ] GoDoc comments on all exported types and functions
- [ ] Example code in `pkg/bridgeclient/example_test.go`

---

## Phase 4: Security Hardening

**Goal**: Production-grade security controls.

### 4.1 Secret Redaction
- [ ] Configurable regex patterns for secret detection
- [ ] Apply redaction to all event text before buffering
- [ ] Apply redaction to all log output
- [ ] Never log JWT tokens, TLS private keys, or provider API keys

### 4.2 Rate Limiting
- [ ] Per-client rate limiting on `StartSession` (token bucket)
- [ ] Per-session rate limiting on `SendInput`
- [ ] Global rate limiting on total RPC calls
- [ ] Return `RESOURCE_EXHAUSTED` gRPC status on limit hit

### 4.3 Audit Logging
- [ ] Log all auth decisions (success and failure) with structured fields
- [ ] Log session lifecycle events (start, stop, crash)
- [ ] Log policy enforcement decisions (limit reached, path denied)
- [ ] Include `project_id`, `session_id`, `caller_cn`, `caller_sub` in all audit entries

### 4.4 Certificate Rotation
- [ ] `bridge-ca renew` command for certificate renewal
- [ ] Bridge daemon watches cert files for changes, reloads without restart
- [ ] SDK client supports cert file watching and reconnect

### 4.5 Input Validation
- [ ] Validate all string fields for control characters
- [ ] Enforce max lengths on all string fields
- [x] Validate `repo_path` is absolute and under allowed paths
- [x] Validate `provider` is a known registered provider
- [ ] Validate `session_id` format (UUID)

---

## Phase 5: Integration Support

**Goal**: Detailed integration guides and helper code for both consumer projects.

### 5.1 prd-manager-control-plane Integration
- [ ] Write integration guide: step-by-step migration from `agent.Manager` to `bridgeclient`
- [ ] Provide example config additions for `bridge_target`, cert paths
- [ ] Document certificate setup: generate certs, cross-sign, build bundle
- [ ] Provide adapter code mapping `bridgeclient` events → `agent.Event`

### 5.2 ndara-ai-orchestrator Integration
- [ ] Write integration guide: replacing stub `agentd` handlers with bridge-backed handlers
- [ ] Provide event mapping: `bridge.v1.SessionEvent` → `orch.v1.Event`
- [ ] Document dual-trust setup (ndara mTLS ↔ agentd mTLS, plus agentd ↔ bridge mTLS)
- [ ] Example `bridge.yaml` for ndara deployment topology

### 5.3 Dev Environment
- [x] `scripts/dev_certs.sh` - Generate all dev certs for local testing
- [x] `docker-compose.yaml` (optional) - Run bridge + mock agents for integration testing
- [x] `Makefile` target: `make dev-setup` - one-command dev environment
- [x] `Makefile` target: `make test-e2e` - dockerized end-to-end validation

---

## Phase 6: Testing & Reliability

**Goal**: Comprehensive test coverage and operational tooling.

### 6.1 Unit Tests
- [x] Session start/stop state transitions
- [x] Input routing and write failures
- [x] Event sequencing and buffer overflow
- [x] Policy enforcement (limits, timeouts, path validation)
- [x] Provider registry and adapter selection
- [x] JWT mint/verify with Ed25519
- [x] mTLS config generation and validation
- [x] CA operations (init, issue, cross-sign, bundle)
- [x] gRPC server authorization and error mapping (`internal/server/server_test.go`)
- [x] Provider startup timeout behavior (`internal/provider/stdio_test.go`)
- [ ] Secret redaction patterns
- [ ] Config loading and validation

### 6.2 Integration Tests
- [x] End-to-end: start → input → output → stop (with real process)
- [ ] Reconnect from `after_seq` (disconnect, reconnect, verify no event loss)
- [ ] Multi-provider concurrent sessions
- [x] mTLS rejection (bad cert, expired cert, wrong CA)
- [x] JWT rejection (expired, wrong audience, wrong issuer)
- [ ] Session limit enforcement
- [x] Provider unavailability handling
- [ ] Graceful shutdown (in-flight sessions drained)

### 6.3 Failure Tests
- [ ] Agent process crash → `SESSION_FAILED` event emitted
- [ ] Bridge daemon restart → all sessions marked failed
- [ ] Network partition simulation (client disconnect/reconnect)
- [ ] Invalid input handling (oversized, malformed)
- [ ] Concurrent session operations (race condition testing)

### 6.4 Observability
- [x] Structured JSON logging with `slog`
- [ ] Metrics (optional): `sessions_active`, `events_total`, `events_dropped`, `rpc_latency_ms`, `auth_failures`
- [ ] gRPC reflection enabled in dev mode (disabled in production)

---

## Dependency Summary

### Direct Dependencies
- `google.golang.org/grpc` - gRPC framework
- `google.golang.org/protobuf` - Protobuf runtime
- `github.com/golang-jwt/jwt/v5` - JWT library (matches ndara-orchestrator)
- `golang.org/x/crypto` - Ed25519 support
- `gopkg.in/yaml.v3` - Configuration parsing

### Dev/Build Dependencies
- `buf` or `protoc` + `protoc-gen-go` + `protoc-gen-go-grpc` - Protobuf code generation
- `golangci-lint` - Linting

---

## Milestone Summary

| Phase | Deliverable | Dependencies |
|---|---|---|
| Phase 1 | Bridge daemon with codex adapter, gRPC API, mTLS+JWT, CA tool | None |
| Phase 2 | Claude + OpenCode adapters, provider health | Phase 1 |
| Phase 3 | Go SDK (`bridgeclient`) | Phase 1 |
| Phase 4 | Security hardening (redaction, rate limiting, audit, rotation) | Phase 1 |
| Phase 5 | Integration guides for both consumer projects | Phase 3 |
| Phase 6 | Comprehensive test suite, observability | Phase 1-4 |

Phases 2, 3, and 4 can be developed in parallel after Phase 1 is complete.
