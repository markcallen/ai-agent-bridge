# AI Agent Bridge - Post-MVP Plan

This document tracks all remaining implementation work after the MVP.
For the completed MVP scope, see [MVP_PLAN.md](MVP_PLAN.md).

---

## Phase 1: Provider Completions

**Goal**: Close out gaps in provider support and session lifecycle management.

### 1.1 Claude Output Parsing
- [x] Parse Claude-specific streaming JSON output format into structured `EventType` fields
  - Detect tool use, content blocks, thinking tokens separately
  - Map `AGENT_READY` / `RESPONSE_COMPLETE` signals to `SESSION_STARTED` / done events
- [x] Integration test: start → send prompt → receive parsed events → stop

### 1.2 OpenCode Integration Test
- [x] Integration test: start → send prompt → receive events → stop
- [ ] Validate PTY interaction model if opencode requires a terminal

### 1.3 Session Idle Timeout
- [x] Enforce `sessions.idle_timeout` from config: stop session automatically after no `SendInput` for the configured duration
- [x] Emit `SESSION_STOPPED` event with reason `"idle_timeout"` on automatic stop
- [x] Unit test: verify idle timeout fires and cleans up session state

### 1.4 Provider Binary Version Detection
- [x] Add `Version(ctx context.Context) (string, error)` to provider `Health` or a separate method
- [x] Call `<binary> --version` and parse output during `Health()` check
- [x] Surface version in `ListProviders` response
- [x] Log provider version at startup

---

## Phase 2: Security Completions

**Goal**: Close remaining security gaps identified during MVP.

### 2.1 Sensitive Value Logging Guard
- [ ] Audit all log call sites: ensure JWT token strings, TLS private key PEM, and provider API keys are never logged
- [ ] Add `redact.Sensitive(val string) string` helper that returns `"[REDACTED]"` for use in log calls
- [ ] Add lint rule or test that fails if a raw JWT or key PEM appears in log output

### 2.2 Input Field Max Lengths
- [ ] Enforce max length on `project_id` (e.g. 128 chars), `session_id` (UUID = 36), `repo_path` (4096), `provider` (64), `text` (already 64KB)
- [ ] Return `INVALID_ARGUMENT` with field-specific message on violation
- [ ] Unit tests for each field boundary

### 2.3 Audit Logging Completions
The MVP wires `project_id`, `session_id`, `caller_cn`, `caller_sub` into audit entries. The following event types are not yet emitted:
- [ ] Session lifecycle audit: log structured entry on `StartSession`, `StopSession`, crash (`SESSION_FAILED`)
- [ ] Policy enforcement audit: log entry when a session limit, path deny, or rate limit fires
- [ ] Confirm auth decision audit (success + failure) is emitted for every RPC, not just interceptor-level

### 2.4 CRL Distribution Point
- [ ] Serve a CRL endpoint from the bridge daemon (`GET /crl` on a separate HTTP port or via gRPC reflection)
- [ ] `ai-agent-bridge-ca` generates a CRL file on `init` and `revoke`
- [ ] Bridge TLS config references CRL for client cert revocation checks
- [ ] Add `ai-agent-bridge-ca revoke --cert <path>` subcommand

---

## Phase 3: Certificate Management

**Goal**: Full certificate lifecycle tooling as specified in PRD section 12.

### 3.1 `ai-agent-bridge-ca renew` Command
- [ ] Implement `renew` subcommand in `cmd/bridge-ca/main.go`
  - Re-issue a certificate with the same CN and SANs but a new validity period
  - Accept `--cert`, `--ca`, `--ca-key`, `--out` flags
  - Validate the existing cert is within a renewal window (default: renew if <30 days remaining)
- [ ] Unit test: renew a cert and verify new expiry

### 3.2 Bridge Daemon Cert Hot-Reload
- [ ] Watch cert and key files for modification using `fsnotify`
- [ ] On file change, reload `tls.Config` and apply to the running gRPC server without restart
- [ ] Log successful reload and any reload errors
- [ ] Integration test: replace cert file, verify bridge accepts new client connections without downtime

### 3.3 SDK Client Cert Watching
- [ ] Watch client cert and key files for modification in `bridgeclient`
- [ ] On change, rebuild `tls.Credentials` and reconnect gRPC channel
- [ ] Expose `WithCertWatcher()` option in `pkg/bridgeclient/options.go`

---

## Phase 4: SDK Completions

**Goal**: Fill remaining gaps in the `bridgeclient` package.

### 4.1 Persistent Subscriber Cursor
- [ ] `pkg/bridgeclient/cursor_store.go` currently tracks cursors in-process only
- [ ] Add file-backed cursor store: persist `last_ack_seq` per `(subscriber_id, session_id)` to a local file
- [ ] Load persisted cursor on startup; use it as `after_seq` on first connect
- [ ] Expose `WithCursorFile(path string)` option
- [ ] Document at-least-once delivery semantics and idempotent consumer callback guidance

### 4.2 Connection Keepalive
- [ ] Configure gRPC keepalive parameters in `pkg/bridgeclient/client.go`
  - `KeepaliveParams`: `Time`, `Timeout`, `PermitWithoutStream`
  - `KeepaliveEnforcementPolicy` on server side
- [ ] Expose `WithKeepalive(cfg)` option

### 4.3 GoDoc Comments
- [ ] Add GoDoc comments to all exported types, functions, and constants in `pkg/bridgeclient/`
- [ ] Verify `go doc` output reads cleanly for each exported symbol

---

## Phase 5: Integration Support

**Goal**: Guides and adapter code for the two primary consumer projects.

### 5.1 General Consumer Integration Guide
- [ ] Write `docs/integration-guide.md`: step-by-step setup of `bridgeclient` in a consumer app
  - Module dependency, config struct additions (`bridge_target`, cert paths, JWT key path)
  - Certificate setup walkthrough: `ai-agent-bridge-ca init`, `issue`, `cross-sign`, `bundle`
  - Code snippets: create client, start session, send input, stream events, stop session
  - Event type mapping examples for common consumer patterns
  - Multi-tenant trust setup for multiple consumer projects

### 5.2 ndara-ai-orchestrator Integration Guide
- [ ] Write `docs/ndara-integration.md`: specific guide for `agentd` daemon integration
  - Map `RunTask` → `StartSession` + `SendInput`
  - Map `StreamEvents` → bridge `StreamEvents` (proxy `orch.v1.Event` ↔ bridge events)
  - Map `CancelRun` → `StopSession`
  - Show how `agentd` authenticates to the bridge as a separate trust boundary
  - Provide example config diff for `agentd/main.go`

### 5.3 prd-manager-control-plane Integration Guide
- [ ] Write `docs/prd-manager-integration.md`: specific guide for replacing `internal/agent/Manager`
  - Map `agentManager.Start` → `bridgeClient.StartSession`
  - Map `agentManager.Send` → `bridgeClient.SendInput`
  - Map `agentManager.Stop` → `bridgeClient.StopSession`
  - Map `agentManager.SetEventHandler` → goroutine calling `bridgeClient.StreamEvents`
  - Show required config additions and cert setup
  - Document `provider` field addition to session API requests

---

## Phase 6: Testing & Reliability

**Goal**: Comprehensive test coverage for correctness and operational robustness.

### 6.1 Reconnect / Replay Integration Tests
- [ ] E2E reconnect from `after_seq`: disconnect stream mid-session, reconnect with last seen seq, verify no event loss and no duplicates
- [ ] E2E disconnect while agent produces events: verify queued events delivered in order on reconnect
- [ ] E2E subscriber cursor persistence: terminate SDK process, restart with cursor file, verify resume from last ack

### 6.2 Failure Tests
- [ ] Agent process crash → `SESSION_FAILED` event emitted to all active subscribers
- [ ] Bridge daemon restart → verify all sessions are cleaned up and clients receive appropriate errors on reconnect
- [ ] Network partition simulation: drop connection while agent runs, reconnect, verify event continuity
- [ ] Concurrent session operations: run with `-race` flag under load, verify no data races

### 6.3 Scenario Tests
- [ ] Session limit enforcement: exceed per-project and global limits, verify `RESOURCE_EXHAUSTED` response
- [ ] Multi-provider concurrent sessions: start codex + claude + opencode simultaneously, verify isolation
- [ ] Graceful shutdown: `SIGTERM` bridge with active sessions, verify in-flight sessions drain and `SESSION_STOPPED` events are emitted

### 6.4 Provider Matrix Tests
- [ ] Run start → input → stop integration test against each of: `codex`, `claude`, `opencode`
- [ ] Verify health check correctly detects missing binary for each provider

---

## Phase 7: Observability

**Goal**: Operational visibility into bridge health and performance.

### 7.1 Prometheus Metrics
- [ ] Expose `/metrics` endpoint (separate HTTP port, unauthenticated)
- [ ] Metrics to instrument:
  - `bridge_sessions_active` (gauge, labeled by `project_id`, `provider`)
  - `bridge_events_total` (counter, labeled by `project_id`, `provider`, `event_type`)
  - `bridge_events_dropped_total` (counter, labeled by `project_id` — buffer overflow)
  - `bridge_rpc_duration_seconds` (histogram, labeled by `method`)
  - `bridge_auth_failures_total` (counter, labeled by `failure_reason`)

### 7.2 OpenTelemetry Tracing
- [ ] Add OTel trace spans on all gRPC RPC handlers
- [ ] Propagate trace context from incoming gRPC metadata
- [ ] Export to configurable OTLP endpoint (env: `OTEL_EXPORTER_OTLP_ENDPOINT`)

### 7.3 Health Endpoint Enhancements
- [ ] Extend `Health` RPC response with detailed provider diagnostics:
  - Provider binary path, version string, last health check timestamp, last error
- [ ] Add readiness vs liveness distinction

### 7.4 gRPC Reflection (Dev Mode)
- [ ] Enable `grpc/reflection` registration when `logging.level = "debug"` or `--dev` flag is set
- [ ] Disable in production builds / when flag not set

### 7.5 Agent Introspection API
- [ ] Add `InspectSession` RPC (or extend `GetSession`) to return live session state:
  - Current provider process PID, uptime, stdin buffer depth
  - Last event timestamp, last error, pending input count
  - Subscriber list with cursor positions
- [ ] Add to protobuf definition and implement in server

---

## Dependency Summary (additional)

| Package | Purpose |
|---|---|
| `github.com/fsnotify/fsnotify` | File watching for cert hot-reload (Phase 3) |
| `github.com/prometheus/client_golang` | Prometheus metrics (Phase 7.1) |
| `go.opentelemetry.io/otel` | OTel tracing (Phase 7.2) |
| `google.golang.org/grpc/reflection` | gRPC reflection (Phase 7.4) |

---

## Phase 8: Antigravity CLI Migration (Gemini → `agy`)

**Goal**: Replace `@google/gemini-cli` with Google's Antigravity CLI (`agy`) before the June 18, 2026 deprecation deadline.

**Background**: Google is retiring Gemini CLI for individual/Pro users on June 18, 2026 in favour of Antigravity CLI. See the [transition announcement](https://developers.googleblog.com/an-important-update-transitioning-gemini-cli-to-antigravity-cli/).

**Key differences requiring work**:

| Aspect | Gemini CLI | Antigravity CLI |
|---|---|---|
| Distribution | npm `@google/gemini-cli` | Native Go binary via `curl … \| bash` |
| Binary | `./node_modules/.bin/gemini` | `agy` (`~/.local/bin/agy`) |
| Auth | `GEMINI_API_KEY` env var | OAuth2 credentials (`~/.gemini/oauth_creds.json`) |
| Model flag | `-m gemini-2.5-flash` arg | Configured in `~/.gemini/antigravity-cli/settings.json` |
| Config dir | `~/.gemini/` | `~/.gemini/antigravity-cli/` |

### 8.1 Resolve e2e / Docker auth strategy (BLOCKER)

The biggest open question: `agy` uses browser-based OAuth2 rather than an API key, which breaks the current Docker/CI flow that passes `GEMINI_API_KEY` as an env var.

Options to investigate:

- **Mount credentials file**: volume-mount `~/.gemini/oauth_creds.json` into the container and set `HOME` or the config path accordingly. Credentials contain a long-lived refresh token so this may work for CI if the token is stored as a secret.
- **Service account / Workload Identity**: determine whether `agy` supports a non-interactive auth path (e.g. `GOOGLE_APPLICATION_CREDENTIALS` ADC, a service account JSON, or Workload Identity for GCP CI). The binary references `GOOGLE_API_USE_CLIENT_CERTIFICATE` internally — worth testing.
- **`agy install` headless flow**: the `agy install` subcommand configures shell paths; check if it also supports a `--token` or `--credentials-file` flag for headless setup.
- **Pre-bake credentials in image**: bake a CI-only OAuth token into the Docker image via a build secret. Least preferred due to credential rotation overhead.

**This item must be resolved before any other Phase 8 work merges.**

### 8.2 Remove `@google/gemini-cli` npm dependency

- Remove `@google/gemini-cli` from `dependencies` in `package.json`
- Run `npm install` to update `package-lock.json`
- Confirm `node_modules/@google/gemini-cli` is no longer present

### 8.3 Update provider configs

Files: `config/bridge.yaml`, `config/bridge-dev.yaml`, `config/bridge-docker.yaml`, `e2e/bridge-e2e.yaml`

Changes to the `gemini:` provider block in each:
- `binary`: update from `./node_modules/@google/gemini-cli/dist/index.js` / `./node_modules/.bin/gemini` to `agy` (or absolute path)
- `args`: remove `-m gemini-2.5-flash` — model is now set in `settings.json`
- `required_env`: remove `GEMINI_API_KEY` — replace with whatever auth mechanism is chosen in 8.1
- `prompt_pattern`: verify the interactive prompt regex still matches `agy`'s prompt (currently `">"` or `"^\s*>\s*$"`)

### 8.4 Update Dockerfile and Docker Compose

Files: `Dockerfile`, `docker-compose.yml`, `e2e/docker-compose.yml`

- Add `agy` install step to `Dockerfile` using the official install script:
  ```sh
  RUN curl -fsSL https://antigravity.google/cli/install.sh | bash
  ```
- Remove `GEMINI_API_KEY` env var from both compose files
- Add whatever auth mechanism is resolved in 8.1 (volume mount, build secret, or env var for ADC)

### 8.5 Update e2e test runner

File: `e2e/cmd/e2e-test/main.go`

- Remove `requiredEnv: "GEMINI_API_KEY"` from the gemini test case
- Update `promptRe` if the `agy` prompt pattern differs from the current `(?m)>\s*$`
- Update `name` field if the provider key is renamed from `gemini` to `antigravity`

### 8.6 Update documentation

Files: `README.md`, `examples/README.md`

- Replace `@google/gemini-cli` install instructions with `agy` install command
- Replace `GEMINI_API_KEY=` with the new auth setup instructions
- Update any provider name references

### 8.7 Rename provider key (optional)

The provider is currently keyed as `gemini:` in all config files. Consider renaming to `antigravity:` for clarity. This is a breaking change for any consumers passing `provider: "gemini"` in API requests — assess impact before doing it.

---

## Milestone Summary

| Phase | Deliverable | Dependencies |
|---|---|---|
| Phase 1 | Provider completions (parsing, idle timeout, version detection) | MVP |
| Phase 2 | Security completions (logging guard, field lengths, audit, CRL) | MVP |
| Phase 3 | Certificate management (`renew`, hot-reload, SDK cert watch) | Phase 2 |
| Phase 4 | SDK completions (persistent cursor, keepalive, GoDoc) | MVP |
| Phase 5 | Integration guides (general, ndara, prd-manager) | Phase 4 |
| Phase 6 | Testing & reliability (reconnect, failure, scenario, matrix) | Phase 1-3 |
| Phase 7 | Observability (metrics, tracing, reflection, introspection) | MVP |
| Phase 8 | Antigravity CLI migration (replace Gemini CLI before June 18, 2026) | 8.1 must land first |

Phases 1, 2, 4, and 7 can be worked in parallel.
Phase 3 depends on Phase 2 (security audit informs what to watch/reload).
Phase 5 should follow Phase 4 (SDK API stable before writing guides).
Phase 6 should follow Phases 1–3 (test against completed feature set).
Phase 8 is time-boxed: 8.1 (auth strategy) must be resolved first; 8.2–8.7 can then proceed in parallel.
