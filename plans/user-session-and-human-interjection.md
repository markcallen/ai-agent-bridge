User Session Model and Human Interjection

## Summary

This plan replaces the system daemon model with a user-session model and adds
concurrent multi-observer session support for human interjection.

The system daemon (`cmd/bridge`) runs as a separate system user with
`ProtectHome=read-only`. It has no access to `$DISPLAY`, `$WAYLAND_DISPLAY`,
`$XDG_RUNTIME_DIR`, X11 auth cookies, or the user's home directory. AI agent
CLIs building web or mobile apps require all of these — they spawn browsers for
testing, launch Android emulators, write to `~/.claude`, read
`$ANTHROPIC_API_KEY`. The daemon model is structurally incompatible with this
use case and must be retired.

The replacement is `bridgectl server start --listen` running as a **systemd
user service** (Linux) or **LaunchAgent** (macOS), inside the login session
where windowing and credentials are available. This is not a configuration
change — it is a deployment model change: the process that manages agent
sessions must run under the login user, not a service account.

The second goal is human interjection. The current single-client attach model
prevents a human from observing or correcting an agent session without stopping
it. The change introduces concurrent read-only observers alongside a single
active writer, with an explicit writer handoff protocol.

---

## PRD Changes Required

The following sections of `PRD.md` need to be updated as part of this work.
These changes should be made before implementation begins (per AGENTS.md §
Non-trivial tasks step 4).

### Section 3: Goals — add

> - Enable AI agents to build web and mobile applications by running sessions
>   inside the user's login environment with access to display servers,
>   emulators, and user credentials.
> - Support concurrent human observation of and interjection into active agent
>   sessions without stopping the agent.

### Section 4: Non-Goals — remove

Remove "Web UI or dashboard (consumers provide their own)." Replace with:

> - A first-party web UI is deferred but not excluded; the WebSocket adapter
>   (`bridge-client-node`) already provides the foundation.

### Section 5: Users — update

Add:

> - **Human Operators** — engineers who need to observe a running agent session
>   in real time, optionally inject a correction or question, then return
>   control to the automated flow without restarting the session.

### Section 6.2: Operational Modes — full replacement

Replace the current two-mode table (Local Server / Daemon / Mixed-Mode) with
the following single-mode description:

---

**User Session Server (`bridgectl server start [--listen]`)**

The bridge runs as the login user inside an interactive or graphical session.
This is the only deployment mode. There is no separate system daemon.

| Property | Behaviour |
|---|---|
| Binary | `cmd/bridgectl` |
| Process context | Login session of the operating user |
| Windowing access | Full — inherits `$DISPLAY`, `$WAYLAND_DISPLAY`, `$XDG_RUNTIME_DIR` |
| Credential source | Inherits the user's shell environment and native CLI auth |
| Local access | Unix socket at `~/.ai-agent-bridge/server.sock`, no auth |
| Remote access | TCP with auto-generated mTLS + JWT (`--listen <addr>`) |
| Startup | Systemd user service (Linux) or LaunchAgent (macOS) |
| Persistence | Optional BoltDB session store (`--db-path`) |
| Intended operator | Any user who needs to run AI agents locally or expose them remotely |

**Why the login session is required**: AI agent CLIs (claude, codex, opencode,
gemini) are user-space programs that need access to home directories, auth
tokens, and on graphically-capable machines, running display servers and device
emulators. A system daemon running as a service account cannot provide this
without recreating the user's environment, which reintroduces all the trust
problems mTLS is designed to eliminate.

**Remote access model**: when `--listen` is set, the server binds to the
specified TCP address and generates PKI material in `~/.ai-agent-bridge/certs/`
on first start. SDK clients authenticate with mTLS + JWT. Human operators
authenticate using OIDC via `bridgectl server issue-client --oidc` (see
Security Architecture). The server must be reachable via WireGuard or Tailscale;
it must not be exposed to the public internet.

**Session persistence**: with `--db-path`, the server writes session metadata
and PTY output chunks to a BoltDB file. On restart, `LoadHistory()` rehydrates
sessions so SDK clients can reconnect and replay events they missed.

---

### Section 6.2 — add subsection: Human Interjection

Human operators can observe and interact with any running session without
stopping it. The session model distinguishes two roles:

**Observer** (`--observe` flag on `bridgectl session attach`): receives the
live PTY output stream and the full replay buffer. Multiple observers may be
attached simultaneously. Observers cannot write input to the session.

**Active Writer**: exactly one client holds the active writer slot at a time.
The writer can send input via `WriteInput` and resize the terminal via
`ResizeSession`. By default, the client that calls `StartSession` becomes the
active writer. A human operator can claim the writer slot with
`bridgectl session attach --take-over <id>`, which transitions the previous
writer to observer role. Returning control is done with Ctrl-] (detach key),
which releases the writer slot and restores the previous active client.

Writer transition protocol:
1. Human runs `bridgectl session attach --take-over <id>`.
2. Server sends a `WRITER_CLAIMED` event to all observers including the SDK.
3. Human types, resizes, or reads.
4. Human presses Ctrl-] or `bridgectl session attach --release <id>`.
5. Server sends a `WRITER_RELEASED` event to all observers.
6. SDK may re-claim the writer slot via `ClaimWriter` RPC.

---

### Section 8.1: Service Definition — add RPCs

Add to the gRPC service definition:

```
rpc ClaimWriter(ClaimWriterRequest) returns (ClaimWriterResponse);
rpc ReleaseWriter(ReleaseWriterRequest) returns (ReleaseWriterResponse);
```

Add to `AttachSessionRequest`:

```
AttachRole role = 4;
```

Where `AttachRole` is:

```
enum AttachRole {
  ATTACH_ROLE_UNSPECIFIED = 0; // defaults to WRITER for backwards compat
  ATTACH_ROLE_WRITER = 1;
  ATTACH_ROLE_OBSERVER = 2;
}
```

Add event types `WRITER_CLAIMED` and `WRITER_RELEASED` to `AttachEventType`.

Add `observer_count` and `active_writer_client_id` to `GetSessionResponse`.

---

### Section 16: Future Scope — update

Move "Web UI for bridge status and session management" from deferred to active
scope, noting the WebSocket adapter is already the foundation.

Remove "Persistent event storage (SQLite backend)" — BoltDB is being
implemented as part of this plan.

---

## Phase 1: Consolidate Daemon Features into bridgectl

### Goal

Port the features that exist only in `cmd/bridge/main.go` into `localserver`
and `bridgectl`, then delete `cmd/bridge`.

### Changes

**`internal/localserver/localserver.go` — extend `Config`**

Add fields to `Config`:
- `ConfigPath string` — path to optional YAML config file; if set, loaded and merged with explicit fields
- `DBPath string` — path to BoltDB session store; empty disables persistence
- `ProviderFallbacks map[string][]string` — passthrough to `server.New()`
- `RedactPatterns []string` — log redaction regexes
- `RateLimits server.RateLimitConfig` — overrides hardcoded defaults
- `EventBufferSize int` — overrides hardcoded `8<<20`
- `IdleTimeout time.Duration` — overrides hardcoded `30*time.Minute`

Extend `localserver.Start()` to:
1. If `ConfigPath` is set, call `config.Load(ConfigPath)` and merge into Config fields (explicit fields take precedence over file values)
2. If `DBPath` is set, open `bridge.NewBoltSessionStore(DBPath)` and pass via `bridge.WithStore()` to Supervisor, call `sup.LoadHistory()`
3. Pass `ProviderFallbacks` to `server.New()` instead of hardcoded nil
4. Build redactor from `RedactPatterns` and apply to logger
5. Use configurable rate limits, buffer size, and idle timeout

**`cmd/bridgectl/server.go` — extend `newServerStartCmd()`**

Add flags:
- `--config <path>` — YAML config file (sets `cfg.ConfigPath`)
- `--db-path <path>` — BoltDB session store (sets `cfg.DBPath`)
- `--rate-limit-global-rps <n>` — override global RPS
- `--log-level debug|info|warn|error`
- `--log-format json|text`

**`cmd/bridge/` — delete**

After all features are ported and the system daemon e2e tests are passing via
`bridgectl --listen`, remove the directory.

**`packaging/ai-agent-bridge.service` — delete**

Replace with Phase 3 user service units.

**`Makefile`**

Remove `BRIDGE`, `run`, `dev-run`, `docker-run`, `build-deb` targets that
reference `cmd/bridge`. The `.deb` package should install `bridgectl` and the
user service unit only.

**`internal/localserver/discoverTarget()` — remove daemon fallback**

Remove the `/run/bridge/server.addr` check (lines 576–585 in `localserver.go`).
This path was only for the system daemon.

### Tests

- Existing `localserver` unit tests must continue to pass
- Add `localserver` tests for persistence: start server with DBPath, stop it,
  start again, verify `LoadHistory()` restores session metadata
- Add `localserver` tests for provider fallbacks: register provider A as
  unavailable, verify StartSession falls back to B
- The `e2e/system-daemon/` directory and its test can be removed after cmd/bridge
  is deleted; bridgectl e2e tests cover the equivalent behaviour

### Acceptance Criteria

- `bridgectl server start --config bridge.yaml` loads YAML and applies settings
- `bridgectl server start --db-path ~/.ai-agent-bridge/sessions.db` persists
  sessions across restart
- `make test` passes at ≥ 75% coverage
- `cmd/bridge/` directory does not exist

---

## Phase 2: Multi-Observer Session Model

### Goal

Allow multiple clients to attach to a session concurrently. Exactly one client
holds the active writer slot. Writer slot can be claimed and released without
stopping the session.

### Proto Changes (`proto/bridge/v1/bridge.proto`)

**Add to `AttachEventType` enum:**

```proto
ATTACH_EVENT_TYPE_WRITER_CLAIMED = 7;
ATTACH_EVENT_TYPE_WRITER_RELEASED = 8;
```

**Add `AttachRole` enum:**

```proto
enum AttachRole {
  ATTACH_ROLE_UNSPECIFIED = 0;
  ATTACH_ROLE_WRITER = 1;
  ATTACH_ROLE_OBSERVER = 2;
}
```

**Extend `AttachSessionRequest`:**

```proto
AttachRole role = 4;
```

`UNSPECIFIED` is treated as `ACTIVE` for backwards compatibility.

**Extend `AttachSessionEvent`:**

```proto
string writer_client_id = 15; // set on WRITER_CLAIMED events
```

**Extend `GetSessionResponse`:**

```proto
int32 observer_count = 16;
string active_writer_client_id = 17;
```

**Add new RPCs and messages:**

```proto
rpc ClaimWriter(ClaimWriterRequest) returns (ClaimWriterResponse);
rpc ReleaseWriter(ReleaseWriterRequest) returns (ReleaseWriterResponse);

message ClaimWriterRequest {
  string session_id = 1;
  string client_id = 2;
  bool force = 3; // steal writer slot from current holder
}

message ClaimWriterResponse {
  bool claimed = 1;
  string previous_writer_client_id = 2;
}

message ReleaseWriterRequest {
  string session_id = 1;
  string client_id = 2;
}

message ReleaseWriterResponse {
  bool released = 1;
}
```

Regenerate with `make proto` after proto changes.

### Supervisor Changes (`internal/bridge/supervisor.go`)

Replace the single `attachedClient string` field on managed session state with:

```go
type attachState struct {
    activeWriter  string                       // client_id of active writer, "" if none
    observers     map[string]chan OutputChunk   // client_id → live output channel
    writerCh      chan OutputChunk              // live channel for active writer (nil if no writer)
}
```

**`Attach(sessionID, clientID, role, afterSeq)`** behaviour:
- `OBSERVER`: add `clientID` to `observers` map; error if already present; return AttachState with live channel
- `ACTIVE`: set `activeWriter = clientID` only if `activeWriter == ""`; return `ErrWriterConflict` if another client holds the slot; add to observers map for output
- `UNSPECIFIED`: same as `ACTIVE`

**`Detach(sessionID, clientID)`**: remove from observers; if was activeWriter, clear it; broadcast `WRITER_RELEASED` event to remaining observers

**`ClaimWriter(sessionID, clientID, force)`**:
- If `force == false` and slot is taken: return `ErrWriterConflict` with current holder's ID
- If `force == true`: demote current writer to observer (send `WRITER_CLAIMED` event to it with new holder), set activeWriter = clientID; broadcast `WRITER_CLAIMED` to all observers
- If slot is empty: set activeWriter = clientID; broadcast `WRITER_CLAIMED`

**`ReleaseWriter(sessionID, clientID)`**: if clientID == activeWriter, clear slot; broadcast `WRITER_RELEASED`

**Output fan-out**: when PTY output arrives, append to ByteBuffer and send to all channels in `observers` map (including the active writer's channel). Use non-blocking sends with a small per-observer buffer; slow observers are dropped with a warning log, not held.

**`WriteInput` enforcement**: `server.go` must verify `clientID == session.activeWriter` before accepting input. Return `PERMISSION_DENIED` otherwise with message "not the active writer".

**`ResizeSession` enforcement**: same check as WriteInput.

### Server Changes (`internal/server/server.go`)

- Implement `ClaimWriter` and `ReleaseWriter` RPCs
- Update `AttachSession` handler to read `role` from request; default to ACTIVE if unspecified
- Update `WriteInput` and `ResizeSession` to check active writer

### bridgectl Changes

**`cmd/bridgectl/session.go` — extend `newSessionAttachCmd()`**

Add flags:
- `--observe` — attach as observer (no write access)
- `--take-over` — attach as ACTIVE with `force = true` via `ClaimWriter`
- `--release` — call `ReleaseWriter` and exit

**`cmd/bridgectl/run.go`** — no changes required; run creates sessions as ACTIVE
by default (existing behaviour)

**Detach key (`ctrl-]`) behaviour update**: when detaching from an ACTIVE
attachment, call `ReleaseWriter` before disconnecting so the session is not left
without a potential writer. Current behaviour (just cancel context) is
sufficient for OBSERVER mode.

### Tests (TDD — write failing tests first)

- Unit test `supervisor.Attach()` with concurrent observers: 3 observers all
  receive the same output chunk
- Unit test `supervisor.Attach()` ACTIVE when slot taken: returns
  `ErrWriterConflict`
- Unit test `supervisor.ClaimWriter()` with force=true: previous writer receives
  `WRITER_CLAIMED` event; new writer can send input; old writer's `WriteInput`
  call returns PERMISSION_DENIED
- Unit test `supervisor.ReleaseWriter()`: slot cleared; all observers receive
  `WRITER_RELEASED` event
- Integration test: start session, attach SDK as ACTIVE, attach human as
  OBSERVER, SDK sends input, human observes output, human calls ClaimWriter,
  human sends input, SDK observes, human calls ReleaseWriter, SDK reclaims

### Acceptance Criteria

- Two concurrent `bridgectl session attach --observe` processes both display
  session output without interfering with each other
- `bridgectl session attach --take-over <id>` gives the terminal write access
  and notifies the SDK
- `WriteInput` from the displaced SDK client returns PERMISSION_DENIED
- After `ReleaseWriter`, SDK's `ClaimWriter` call succeeds
- Single-client behaviour (no `--observe` flag) is unchanged

---

## Phase 3: User Session Deployment

### Goal

Replace the system service unit with user-session deployment artifacts so
`bridgectl` starts automatically inside the login session.

### Linux: systemd user service

Create `packaging/bridge.user.service`:

```ini
[Unit]
Description=AI Agent Bridge (user session)
Documentation=https://github.com/markcallen/ai-agent-bridge
After=default.target

[Service]
Type=simple
ExecStart=%h/.local/bin/bridgectl server start
Restart=on-failure
RestartSec=5s
Environment=HOME=%h

[Install]
WantedBy=default.target
```

For graphical machines that need `$DISPLAY` and `$WAYLAND_DISPLAY`, add:

```ini
After=graphical-session.target
PartOf=graphical-session.target
```

Install to `~/.config/systemd/user/bridge.service` or ship it in the package
to `/usr/lib/systemd/user/bridge.service` for `systemctl --user enable bridge`.

**Remote access variant** (separate unit or drop-in):

```ini
[Service]
ExecStart=%h/.local/bin/bridgectl server start --listen 0.0.0.0:9445
```

### macOS: LaunchAgent

Create `packaging/com.markcallen.ai-agent-bridge.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.markcallen.ai-agent-bridge</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/bridgectl</string>
    <string>server</string>
    <string>start</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/tmp/ai-agent-bridge.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/ai-agent-bridge.err</string>
</dict>
</plist>
```

Install to `~/Library/LaunchAgents/`. Load with `launchctl load`.

### Packaging changes

- Remove `packaging/ai-agent-bridge.service` (system unit)
- Remove `User=bridge`, `ProtectHome`, `ProtectSystem` constraints from any
  remaining units — these are incompatible with windowing access
- `.deb` installs `bridgectl` to `/usr/bin/` and the user unit to
  `/usr/lib/systemd/user/`; no longer creates a `bridge` system user or group
- `.deb` post-install prints instructions for `systemctl --user enable --now bridge`
- Homebrew formula (if/when created) uses the LaunchAgent plist

### Makefile

- Add `install-user-service` target: copies plist/unit to the correct location
  and enables it
- Remove `run`, `dev-run` targets referencing `cmd/bridge`

### Acceptance Criteria

- `systemctl --user enable --now bridge` starts the server on login (Linux)
- `launchctl load ~/Library/LaunchAgents/com.markcallen.ai-agent-bridge.plist`
  starts the server on login (macOS)
- `bridgectl server status` reports the running server
- `bridgectl run --provider claude .` works without manual server start
- Agent process has access to `$DISPLAY` when set (test by running a provider
  that opens a browser window)

---

## Phase 4: Security Model

Security model changes are described fully in
`plans/security-architecture.md`. The implementation requirements here are:

- `bridgectl server issue-client` already exists; extend it with
  `--oidc-provider <url>` to enrol human clients via OIDC when Step CA is
  configured
- The OIDC flow for human operators uses Step CA's existing OIDC provisioner;
  no new Go code is required beyond wiring the provisioner URL into
  `EnsurePKI()` as an optional config field
- Auto-generated PKI (`localserver/pki.go`) remains the default for developers
  who do not operate Step CA
- Document the two tiers: self-signed auto-PKI (default) and Step CA integration
  (optional, for teams with existing OIDC infrastructure)

---

## Implementation Order

1. Update `PRD.md` with the changes described above
2. Phase 1 (consolidation): port features, delete cmd/bridge, update packaging
3. Phase 2 (multi-observer): proto changes → supervisor → server → bridgectl CLI
4. Phase 3 (deployment): write unit files, update .deb, update Makefile
5. Update `ARCHITECTURE.md` to reflect the consolidated model
6. Update `plans/security-architecture.md` (done in parallel with this plan)

Each phase ships as its own PR. PRD.md updates go in the Phase 1 PR.

---

## What Is Not Changing

- The gRPC API wire format (existing RPCs are additive changes only)
- The bridgeclient Go SDK public API surface (ClaimWriter/ReleaseWriter are
  additive)
- The unix socket local mode and its no-auth behaviour
- The mTLS + JWT secure mode and its auto-generated PKI
- The WebSocket adapter (`bridge-client-node`) — it already works against
  bridgectl's server
- The `bridge-ca` binary — cert management tooling is independent of the
  server model
