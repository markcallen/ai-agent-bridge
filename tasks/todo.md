## 2026-04-01 Rule Alignment Remediation Plan

# Task: Align ai-agent-bridge with global and Ballast local rules

## Context
- Owner: Codex
- Date: 2026-04-01
- Mode: Approval-Required

## Scope
- In scope:
- Bring repository workflow, tests, docs, release automation, and local-dev setup into line with `~/.agents/AGENTS.md`, `~/.agents/EXECUTION_FRAMEWORK.md`, and repo-local `.codex/rules/`.
- Cover both Go and TypeScript surfaces defined in `.rulesrc.json`.
- Out of scope:
- Feature work unrelated to rule compliance.
- Deep product redesign unless required to satisfy testability, release, or security rules.

## Constraints
- Preserve existing public APIs unless a separate approval is given for breaking changes.
- Do not remove existing Docker, release, or docs flows without a compatible replacement.
- Keep generated protobuf code generated from `proto/`, not manually edited.

## Risks and Tradeoffs
- Risk: Raising coverage from the current baseline to the mandated 75% will require non-trivial test investment across server, provider, and client packages.
- Tradeoff: Prioritize coverage in high-value runtime paths first, then backfill lower-risk packages.
- Risk: Replacing `.env`-based local workflows with `env-secrets` touches docs, startup code, Docker Compose, and contributor workflow at once.
- Tradeoff: Migrate in stages so local development remains usable throughout the transition.
- Risk: Tightening CI, hooks, and release gates may initially slow iteration until the repo is clean.
- Tradeoff: Short-term friction is required to enforce the rule set consistently.

## Execution Checklist
- [ ] Baseline the repo against all applicable rules and convert this plan into tracked implementation issues/workstreams.
- [ ] Replace `.env`-centric local-dev guidance with `env-secrets` guidance and examples; remove any committed workflow assumptions that require plaintext `.env` files.
- [ ] Add or update repo automation for Go linting and testing: `.golangci*`, root `.pre-commit-config.yaml`, installed `pre-commit` hooks, and CI lint plus coverage gates.
- [ ] Raise automated Go coverage to at least 75% overall, with new deterministic regression tests in low-coverage runtime packages (`internal/server`, `internal/bridge`, `internal/provider`, `pkg/bridgeclient`, and command entrypoints where practical).
- [ ] Strengthen GitHub Actions so CI runs build, lint, race tests, coverage enforcement, and smoke checks with clear separation of responsibilities.
- [ ] Bring release automation in line with publishing rules: add workflow-dispatch semver bumping/tagging, validate from tags, and keep app versus SDK/library release paths explicit.
- [ ] Add missing repo automation for dependency maintenance, especially `.github/dependabot.yml` covering npm ecosystems and GitHub Actions.
- [ ] Align docs with the documentation rules: create `docs/README.md` as the index, update `README.md` quick-start and prerequisites, document local-dev, release, troubleshooting, and architecture paths, and verify all commands/paths.
- [ ] Align TypeScript package standards for `packages/bridge-client-node`, `examples/chat-ts`, and `examples/chat-web`: Node version policy, `engines`, lint/test scripts, and CI coverage for the maintained package surfaces.
- [ ] Review structured logging fields and secret-redaction coverage in Go and TypeScript paths; standardize request/session identifiers and avoid leaking secrets in logs.
- [ ] Re-run full verification and capture evidence in this file before opening follow-up PRs.

## Test Strategy
- Unit:
- Add table-driven tests for config, auth, bridge/session logic, provider command construction, websocket/grpc client helpers, and request validation.
- Integration:
- Add focused server/client integration tests for session lifecycle, attach/replay semantics, auth interceptors, and Docker/runtime smoke paths.
- E2E:
- Keep the existing smoke path, but make it validate deployability rather than re-running the generic unit suite.
- Failure-path tests:
- Cover auth failures, invalid config, provider validation failures, replay/attach edge cases, and release workflow misconfiguration where scriptable.

## Rollback Strategy
- Trigger:
- CI hardening or local-dev migration blocks contributors or breaks release workflows without a compatible replacement.
- Rollback steps:
- Revert the specific workflow/hook/doc migration commit while keeping isolated test additions that remain valid.
- Restore prior local-dev entrypoints temporarily, but keep the remediation checklist open.
- Validation after rollback:
- Confirm the previous `make build`, `make test`, Docker, and release paths still work.

## Outcome
- Result:
- Pending.
- Evidence links/commands:
- Pending.

---

# Task: PTY Transport Cutover

## Context
- Owner: Codex
- Date: 2026-03-30
- Mode: Approval-Required

## Scope
- In scope:
- Replace the current event/text transport with a PTY byte transport.
- Redesign the Go gRPC client/server contract around attach, replay, live output, input, and resize.
- Convert Claude into a single interactive PTY provider and support OpenCode and Gemini.
- Add startup checks for required API keys and provider/model validation.
- Update the example chat app and e2e smoke tests to validate prompt, response, and follow-up input flows.
- Out of scope:
- Codex provider support.
- Backward compatibility for the legacy event transport APIs.

## Constraints
- Preserve existing repo path policy and auth posture unless the new PTY design requires narrower changes.
- Keep the user-visible experience aligned with what the provider shows in a local terminal.
- Tests should validate transport fidelity closely enough to catch dropped or rewritten terminal output.

## Risks and Tradeoffs
- Risk: Replacing the proto outright can break any un-migrated client code in the repo.
- Tradeoff: A full cutover avoids carrying a compatibility layer that would distort the PTY design.
- Risk: Startup provider validation may require real network/API access and could add startup latency.
- Tradeoff: Early startup failure is preferable to late session failures for bad credentials/models.

## Execution Checklist
- [ ] Replace the protobuf service and generated client/server code with a PTY-native contract.
- [ ] Implement PTY byte buffering, single-client attach semantics, replay, live streaming, input, and resize.
- [ ] Replace the provider layer with interactive PTY providers for Claude, OpenCode, and Gemini.
- [ ] Add startup env/model validation for configured providers.
- [ ] Rewrite the Go client and example chat app for terminal-like rendering.
- [ ] Rewrite e2e smoke tests for startup prompt, response, and follow-up input flows.
- [ ] Run focused tests and capture evidence.

## Test Strategy
- Unit: PTY buffer, replay/live boundary, single-client attach rules, startup validation parsing.
- Integration: gRPC attach/input/resize lifecycle with a deterministic fake PTY program.
- E2E: Real Claude/OpenCode/Gemini smoke tests for initial prompt, response, and follow-up user input.
- Failure-path tests: invalid attach, second client rejected, replay gap, bad credentials/model validation failures.

## Rollback Strategy
- Trigger: PTY transport fails basic attach/prompt/response scenarios or breaks auth/startup behavior.
- Rollback steps:
- Revert proto, server, and client contract changes together.
- Restore legacy provider/session/event implementations.
- Validation after rollback:
- Run the previous Go test suite and legacy e2e scenarios.

## Outcome
- Result:
- Replaced the legacy event/text transport with a PTY-output attach stream plus input/resize RPCs.
- Reworked providers around interactive PTY startup and added startup-time provider env/probe validation in the daemon.
- Rewrote the example terminal app to pass through remote PTY screens to the local terminal.
- Replaced the legacy e2e harness with PTY-oriented provider smoke scenarios for Claude, OpenCode, and Gemini.
- Evidence links/commands:
- `make proto`
- `go test ./...`
