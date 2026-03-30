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
