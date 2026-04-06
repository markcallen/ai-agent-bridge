# Task: Align Docs and Persistence Workstream

## Context
- Owner: Codex
- Date: 2026-04-05
- Mode: Autonomous

## Scope
- In scope: capture the current action plan that reconciles documentation, prioritizes restart durability, and enumerates verification steps.
- Out of scope: implementation work, code changes, CI runs.

## Constraints
- Keep the plan based on the actual PTY/attach implementation; do not assume the earlier event-driven design is shipping.

## Risks and Tradeoffs
- Risk: downstream contributors may implement stale features if the docs stay wrong; addressing this plan first avoids wasted work.
- Tradeoff: documenting priorities delays starting coding but prevents divergent effort.

---

## Execution Checklist

### 1. Documentation mismatches with the real API

Identified mismatches between `ARCHITECTURE.md` / `docs/` and the actual implementation:

- [x] **`ARCHITECTURE.md` names `EventBuffer` and `SubscriberManager`** — the real types are `ByteBuffer` (`internal/bridge/bytebuf.go`) and a direct single-client `Attach()` model in `supervisor.go`. There is no `SubscriberManager`. Update `ARCHITECTURE.md` to reflect `ByteBuffer` and the `AttachState` channel pattern.

- [x] **Provider mode labels in architecture diagram** — the diagram shows `codex (stdio)` and `claude (stdio)` but `opencode` is PTY, and `claude-chat` is `stream-json`. The actual mode-to-provider mapping is:
  - `claude` → stdio
  - `codex` → exec (JSONL via `codex exec --json -`)
  - `opencode` → PTY
  - `gemini` → PTY
  - `claude-chat` → stream-json
  Update the diagram to show correct modes.

- [x] **`AttachEventType` enum in `docs/service.md`** — docs reference `REPLAY_GAP` but do not document when it fires (buffer wrapped; `OldestSeq` advanced past `after_seq`). Add a note clarifying the replay gap condition.

- [x] **Issue #1 (thinking blocks) not reflected in docs** — `docs/grpc-api.md` and proto reference docs do not mention `EVENT_TYPE_THINKING`. Since issue #1 is open/unimplemented, this is correct; add a note in `docs/grpc-api.md` that thinking-block events are planned (issue #1) but not yet emitted.

- [x] **`docs/go-sdk.md` CursorStore caveat** — the docs describe `FileCursorStore` as enabling durable replay across reconnects, but issue #6 documents that the cursor is useless after a server restart because the session record no longer exists. Add an explicit limitation note: *cursor persistence is within a single server lifetime until issue #6 is resolved.*

### 2. Restart/session persistence gap — phased actions

Root issue tracked in **GitHub issue #6** ("Session state is lost on server restart — no persistence or recovery path").

Current state: all session data lives in `map[string]*managedSession` in `internal/bridge/supervisor.go`. `ByteBuffer` is in-memory only. No restart detection exists.

**Phase 1 — Metadata tracking (short term)** ✅ COMPLETE
- [x] Persist `SessionInfo` to bbolt on every state transition (`internal/bridge/session_store.go`, `supervisor.go`).
- [x] On daemon startup, reload session index via `LoadHistory()`; orphaned (non-terminal) sessions marked `FAILED`.
- [x] Expose server instance UUID in `HealthResponse.server_instance_id` (proto field 3).
- [x] Config: `persistence.db_path` in YAML; `PersistenceConfig` struct; store wired in `cmd/bridge/main.go`.
- [x] Tests: `session_store_test.go` (3 tests), supervisor persistence/orphan tests.

**Phase 2 — Durable buffering (medium term)** ✅ COMPLETE
- [x] Extend `SessionStore` interface with `SaveChunk(sessionID, chunk)` and `LoadChunks(sessionID)`.
- [x] `BoltSessionStore` implements both using a `"chunks"` bbolt bucket (key: `sessionID/seq-16hex`).
- [x] `readLoop` calls `persistChunk` (best-effort) on every PTY byte chunk.
- [x] `Attach()` extended: for history sessions (stopped/failed, in store), loads persisted chunks, returns them as `Replay` with a closed `Live` channel.
- [x] `Detach()` is a no-op for history sessions.
- [x] Config: `persistence.chunk_storage_bytes` field (reserved; enforcement deferred).
- [x] Tests: 3 new chunk store tests + 1 supervisor history-replay test.
- Deferred: active per-session chunk retention enforcement (planned for a follow-on).

**Phase 3 — Restart detection and agent reattach (long term)**
- On daemon startup, scan for orphaned child PIDs from the previous run (via PID file or `/proc` inspection).
- If an agent process is still alive, re-open its PTY or pipe and resume buffering.
- Reconnect semantics: client reconnects → `GetSession` returns `RUNNING` with updated `server_instance_id` → client resumes attach from stored cursor.
- Key files: `cmd/bridge/main.go`, `internal/bridge/supervisor.go`, `internal/provider/`.

### 3. Open issue alignment

Issues referenced in original plan were #1, #4, #8. Current open issues after review:

| Issue | Title | Status | Action |
|-------|-------|--------|--------|
| **#1** | Stream agent thinking blocks to SDK | ✅ COMPLETE | `ATTACH_EVENT_TYPE_THINKING=6` in proto; `claude-chat` stream-JSON provider; `readLoopStreamJSON`; `closeLive`; `ByteBuffer.After` type fix. |
| **#2** | Migrate e2e tests to testify/suite | Open — unimplemented | Keep open. Migration steps clearly defined in issue. Not a blocker for persistence work. |
| **#4** | `mode: exec` hardcoded to `CodexExecProvider` | Open bug | Keep open. Fix: validate that `mode: exec` is only permitted for `codex` provider at startup and return a config error otherwise. Low risk today but fragile as more exec-style providers are added. |
| **#6** | Session state lost on server restart | Open enhancement | **Primary persistence issue.** Phase 1–3 above maps directly to the short/medium/long-term proposals in this issue. |
| **#8** | Model fallback handling | Open enhancement | Keep open. Large scope (fallback config, scheduled version-check automation, expanded smoke tests). Not a blocker for docs or persistence work. Implement after Phase 1 of persistence. |

**Correction from original plan**: The original checklist referenced issues #1, #4, #8. Issue #6 (not #8) is the session persistence issue. Issue #8 is the model fallback feature. Both are in scope but #6 is the persistence priority.

---

## Test Strategy
- Unit: not applicable for plan entry.
- Integration: not applicable.
- E2E: not applicable.
- Failure-path tests: not applicable.

## Rollback Strategy
- Trigger: plan is outdated or superseded by new direction.
- Rollback steps: revise `tasks/update-plan.md` with updated priorities.
- Validation after rollback: confirm plan reflects latest repo state.

## Outcome
- Docs reconciliation: ✅ ARCHITECTURE.md, grpc-api.md, go-sdk.md, service.md
- Issue #4 (mode field): ✅ config validation rejects non-empty mode at startup
- Issue #6 Phase 1 (server UUID + bbolt metadata): ✅ server_instance_id in HealthResponse; bbolt SessionStore with LoadHistory and orphan detection
- Issue #6 Phase 2 (durable PTY chunks): ✅ SaveChunk/LoadChunks; Attach() serves history sessions read-only from persisted chunks
- Evidence: `tasks/update-plan.md`, commits on `docs-and-persistence-phase1` branch.

## Remaining Open Work
- **Issue #6 Phase 3** (long-term): process orphan reattach, PID scanning on restart — deferred
- **Issue #8**: Model fallback handling — large scope, deferred
