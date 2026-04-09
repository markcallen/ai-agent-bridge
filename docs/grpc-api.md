# gRPC API Reference

> Machine-readable summary: Service `bridge.v1.BridgeService`. Proto: `proto/bridge/v1/bridge.proto`. Default endpoint: `127.0.0.1:9445`. Auth: mTLS + JWT bearer token in gRPC metadata. All session IDs must be UUIDs. `data` fields are raw bytes (base64 in JSON/grpcurl).

---

## Service Definition

```
package bridge.v1
service BridgeService
```

Import path for code generation: `proto/bridge/v1/bridge.proto`

Go generated package: `github.com/markcallen/ai-agent-bridge/gen/bridge/v1`

---

## RPCs

### StartSession

Start an AI agent process for a given repository and provider.

```protobuf
rpc StartSession(StartSessionRequest) returns (StartSessionResponse)
```

**Request**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `project_id` | string | yes | Project identifier (bound to JWT claims) |
| `session_id` | string | yes | UUID for this session; must be unique |
| `repo_path` | string | yes | Absolute path to the repository inside the daemon's filesystem |
| `provider` | string | yes | Provider name as configured in `config/bridge.yaml` (e.g. `claude`) |
| `agent_opts` | map<string,string> | no | Provider-specific key/value options passed to the agent |
| `cols` | uint32 | no | Initial PTY width (default: 80) |
| `rows` | uint32 | no | Initial PTY height (default: 24) |

**Response**

| Field | Type | Description |
|-------|------|-------------|
| `session_id` | string | Echo of the requested session ID |
| `status` | SessionStatus | Initial status (typically `STARTING`) |
| `created_at` | Timestamp | Session creation time |

---

### StopSession

Stop a running session gracefully (or forcefully).

```protobuf
rpc StopSession(StopSessionRequest) returns (StopSessionResponse)
```

**Request**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `session_id` | string | yes | Session to stop |
| `force` | bool | no | If true, send SIGKILL immediately instead of waiting for grace period |

**Response**

| Field | Type | Description |
|-------|------|-------------|
| `status` | SessionStatus | Status after the stop request (typically `STOPPING` or `STOPPED`) |

---

### GetSession

Retrieve current metadata for a session.

```protobuf
rpc GetSession(GetSessionRequest) returns (GetSessionResponse)
```

**Request**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `session_id` | string | yes | Session to query |

**Response**

| Field | Type | Description |
|-------|------|-------------|
| `session_id` | string | Session ID |
| `project_id` | string | Owning project |
| `provider` | string | Provider name |
| `status` | SessionStatus | Current status |
| `created_at` | Timestamp | Creation time |
| `stopped_at` | Timestamp | Stop time (if stopped) |
| `error` | string | Error message (if failed) |

---

### ListSessions

List sessions for a project.

```protobuf
rpc ListSessions(ListSessionsRequest) returns (ListSessionsResponse)
```

**Request**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `project_id` | string | no | Filter by project. If empty, returns all sessions the caller is authorized to see. |

**Response**

| Field | Type | Description |
|-------|------|-------------|
| `sessions` | repeated GetSessionResponse | Session list |

---

### AttachSession

Attach to a session. Replays buffered output from `after_seq`, then streams live PTY bytes.

```protobuf
rpc AttachSession(AttachSessionRequest) returns (stream AttachSessionEvent)
```

**Request**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `session_id` | string | yes | Session to attach to |
| `after_seq` | uint64 | no | Resume from this sequence number. `0` = replay all retained output. |
| `client_id` | string | no | Stable client identifier for cursor tracking across reconnects. Auto-generated if empty. |

**Stream events**

Each `AttachSessionEvent` has:

| Field | Type | Description |
|-------|------|-------------|
| `type` | AttachEventType | Event type (see below) |
| `seq` | uint64 | Monotonic sequence number |
| `timestamp` | Timestamp | When the event was produced |
| `session_id` | string | Session this event belongs to |
| `payload` | bytes | Raw PTY bytes (for OUTPUT events) |
| `replay` | bool | `true` if this event is from the ring buffer replay, `false` if live |
| `oldest_seq` | uint64 | Oldest sequence retained in buffer (present on ATTACHED event) |
| `last_seq` | uint64 | Last sequence in buffer at attach time (present on ATTACHED event) |
| `exit_recorded` | bool | Whether an exit code is available (present on SESSION_EXIT) |
| `exit_code` | int32 | Process exit code (present on SESSION_EXIT) |
| `error` | string | Error description (present on ERROR and REPLAY_GAP) |
| `cols` | uint32 | PTY columns (present on ATTACHED) |
| `rows` | uint32 | PTY rows (present on ATTACHED) |

**AttachEventType values**

| Value | Name | Description |
|-------|------|-------------|
| 0 | `UNSPECIFIED` | Should not appear |
| 1 | `ATTACHED` | First event; confirms attachment and delivers buffer metadata (`oldest_seq`, `last_seq`, `cols`, `rows`) |
| 2 | `OUTPUT` | Raw PTY bytes in `payload`; `replay=true` during replay phase, `false` for live output |
| 3 | `REPLAY_GAP` | The requested `after_seq` was evicted from the ring buffer. Replay restarts from `oldest_seq`. Clients should treat the output as incomplete and re-render from the oldest available chunk. |
| 4 | `SESSION_EXIT` | Agent process exited; `exit_code` and `exit_recorded` are set |
| 5 | `ERROR` | Stream error; `error` field contains details |

> **Planned (not yet implemented)**: A future `EVENT_TYPE_THINKING` event (see [issue #1](https://github.com/markcallen/ai-agent-bridge/issues/1)) will surface Claude's extended thinking blocks from the `claude-chat` (stream-json) provider. No `THINKING` events are emitted today.

**Reconnect pattern**

Save the last `seq` you processed. On reconnect, pass it as `after_seq`. If you receive a `REPLAY_GAP` event, the sequence was evicted — you may choose to re-render from the oldest available output.

The Go SDK tracks cursors automatically via `CursorStore` (in-memory by default).

---

### WriteInput

Send bytes to the agent's stdin.

```protobuf
rpc WriteInput(WriteInputRequest) returns (WriteInputResponse)
```

**Request**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `session_id` | string | yes | Target session |
| `client_id` | string | yes | Must match the `client_id` used in `AttachSession` |
| `data` | bytes | yes | Raw bytes to write to the PTY. Max: `input.max_size_bytes` (default 64 KB) |

**Response**

| Field | Type | Description |
|-------|------|-------------|
| `accepted` | bool | Whether the input was accepted |
| `bytes_written` | uint32 | Number of bytes actually written |

---

### ResizeSession

Resize the PTY for an attached session.

```protobuf
rpc ResizeSession(ResizeSessionRequest) returns (ResizeSessionResponse)
```

**Request**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `session_id` | string | yes | Target session |
| `client_id` | string | yes | Must match the `client_id` used in `AttachSession` |
| `cols` | uint32 | yes | New PTY width in columns |
| `rows` | uint32 | yes | New PTY height in rows |

**Response**

| Field | Type | Description |
|-------|------|-------------|
| `applied` | bool | Whether the resize was applied |

---

### Health

Check daemon and provider health.

```protobuf
rpc Health(HealthRequest) returns (HealthResponse)
```

**Request**: empty

**Response**

| Field | Type | Description |
|-------|------|-------------|
| `status` | string | `"serving"` (always, for now) |
| `providers` | repeated ProviderHealth | Per-provider health |
| `server_instance_id` | string | UUID generated once at daemon startup. Compare across calls to detect a restart: a changed value means the process restarted. Persisted sessions and chunks are reloaded on restart; if a prior session's child PID is still alive it is surfaced again as `RUNNING`, but attach/input recovery is currently replay-only. |

`ProviderHealth`:

| Field | Type | Description |
|-------|------|-------------|
| `provider` | string | Provider name |
| `available` | bool | Whether the provider binary is found and configured |
| `error` | string | Error if unavailable |

---

### ListProviders

List configured providers and their availability.

```protobuf
rpc ListProviders(ListProvidersRequest) returns (ListProvidersResponse)
```

**Request**: empty

**Response**

| Field | Type | Description |
|-------|------|-------------|
| `providers` | repeated ProviderInfo | Provider list |

`ProviderInfo`:

| Field | Type | Description |
|-------|------|-------------|
| `provider` | string | Provider name |
| `available` | bool | Whether the provider is ready |
| `binary` | string | Path to the provider binary |
| `version` | string | Binary version (if detectable) |

---

## Enumerations

### SessionStatus

| Value | Name | Description |
|-------|------|-------------|
| 0 | `UNSPECIFIED` | |
| 1 | `STARTING` | Process is starting up |
| 2 | `RUNNING` | Process is running, no client attached |
| 3 | `ATTACHED` | Process is running, client is attached |
| 4 | `STOPPING` | Stop requested, grace period in progress |
| 5 | `STOPPED` | Process has exited cleanly |
| 6 | `FAILED` | Process exited with an error |

---

## Error Codes

The daemon returns standard gRPC status codes:

| Code | Meaning |
|------|---------|
| `NOT_FOUND` | Session ID does not exist |
| `ALREADY_EXISTS` | Session ID already in use |
| `RESOURCE_EXHAUSTED` | Session limit reached or rate limit exceeded |
| `PERMISSION_DENIED` | JWT claims do not match the requested project |
| `UNAUTHENTICATED` | Missing or invalid JWT or client certificate |
| `INVALID_ARGUMENT` | Malformed request (bad UUID, empty required field, oversized input) |
| `FAILED_PRECONDITION` | Another client is already attached to this session |

---

## Code Generation

To generate client stubs in another language from the proto file:

```bash
# Example: generate Python stubs
python -m grpc_tools.protoc \
  -I proto \
  --python_out=. \
  --grpc_python_out=. \
  proto/bridge/v1/bridge.proto
```

The proto file is at `proto/bridge/v1/bridge.proto` in this repository.

---

## Related

- [Service reference](service.md) — Architecture and configuration
- [Go SDK](go-sdk.md) — Typed Go wrapper
- [Node.js SDK](node-sdk.md) — Node.js adapter
