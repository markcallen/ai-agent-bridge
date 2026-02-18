# TODO - Deferred Work

Items tracked here are out of scope for the current implementation but planned for future phases.

## Security Hardening

- [ ] Secret redaction in event streams (configurable regex patterns)
- [ ] Secret redaction in log output
- [ ] Per-client rate limiting on StartSession (token bucket)
- [ ] Per-session rate limiting on SendInput
- [ ] Global RPC rate limiting with RESOURCE_EXHAUSTED responses
- [x] Audit logging of all auth decisions (success and failure)
- [ ] Certificate file watching with hot-reload (no daemon restart)
- [ ] SDK client cert file watching and auto-reconnect
- [ ] CRL distribution point served by bridge daemon
- [ ] OCSP stapling support
- [ ] Input validation for control characters in string fields
- [ ] UUID format validation for session_id

## Provider Enhancements

- [ ] Provider-specific output parsing (JSON events from Claude, structured codex output)
- [x] Provider startup timeout enforcement (currently configured but not enforced)
- [ ] Session idle timeout with automatic cleanup
- [ ] Provider binary version detection via `--version` health check
- [ ] `gemini` adapter (future provider)
- [ ] `droid` adapter (future provider)
- [ ] Plugin system for custom provider adapters

## Persistence & Storage

- [ ] SQLite backend for persistent event storage (survive restarts)
- [ ] Session metadata persistence (survive bridge daemon restart)
- [ ] Event export to external systems (webhook, Kafka, etc.)

## Observability

- [ ] Prometheus metrics: `sessions_active`, `events_total`, `events_dropped`, `rpc_latency_ms`, `auth_failures`
- [ ] OpenTelemetry trace spans on RPC calls
- [ ] Structured audit log with `project_id`, `session_id`, `caller_cn`, `caller_sub` (partial: `caller_cn` not yet wired)
- [ ] gRPC reflection (dev mode only)
- [ ] Health endpoint with detailed provider diagnostics

## SDK (bridgeclient)

- [ ] Configurable retry policy with exponential backoff
- [ ] Connection keepalive configuration
- [ ] SDK-level context propagation (trace IDs)
- [ ] Example integration test with real bridge daemon
- [ ] GoDoc examples (`example_test.go`)

## Integration

- [ ] Detailed migration guide for prd-manager-control-plane
- [ ] Detailed migration guide for ndara-ai-orchestrator
- [ ] Event type mapping: bridge events → `agent.Event` (prd-manager)
- [ ] Event type mapping: bridge events → `orch.v1.Event` (ndara)
- [ ] Feature flag for gradual rollout (in-process fallback)
- [x] Docker Compose setup for integration testing
- [x] `make dev-setup` one-command dev environment with all certs

## Testing

- [x] Integration tests: full gRPC round-trip (start → input → events → stop)
- [ ] Integration tests: mTLS rejection (bad cert, expired, wrong CA)
- [ ] Integration tests: JWT rejection (expired, wrong audience, wrong issuer)
- [ ] Integration tests: reconnect/replay with after_seq
- [ ] Failure tests: agent process crash detection
- [ ] Failure tests: bridge daemon restart (session cleanup)
- [ ] Failure tests: concurrent session operations (race detection)
- [ ] Load testing: concurrent sessions and event throughput
- [ ] Matrix coverage: codex × claude × opencode providers

## Multi-Language SDKs

- [ ] Python SDK (generated from protobuf + thin wrapper)
- [ ] TypeScript SDK (generated from protobuf)
- [ ] Publish protobuf definitions as standalone package

## Infrastructure

- [ ] SPIFFE/SPIRE integration for workload identity
- [ ] Multi-bridge clustering and session migration
- [ ] Policy-as-code (OPA/Rego) for authorization
- [ ] Web UI for bridge status and session management
- [ ] Systemd unit file / Docker image for deployment
- [ ] Helm chart for Kubernetes deployment
