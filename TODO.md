# TODO - Deferred Work

Items tracked here are out of scope for the current implementation but planned for future phases.
Items already covered in [PLAN.md](PLAN.md) are tracked there only.

## Security Hardening

- [x] Secret redaction in event streams (configurable regex patterns)
- [x] Secret redaction in log output
- [x] Per-client rate limiting on StartSession (token bucket)
- [x] Per-session rate limiting on SendInput
- [x] Global RPC rate limiting with RESOURCE_EXHAUSTED responses
- [x] Audit logging of all auth decisions (success and failure)
- [x] Input validation for control characters in string fields
- [x] UUID format validation for session_id
- [ ] OCSP stapling support

## Provider Enhancements

- [x] Provider startup timeout enforcement (currently configured but not enforced)
- [ ] `gemini` adapter (future provider)
- [ ] `droid` adapter (future provider)
- [ ] Plugin system for custom provider adapters

## Persistence & Storage

- [ ] SQLite backend for persistent event storage (survive restarts)
- [ ] Session metadata persistence (survive bridge daemon restart)
- [ ] Event export to external systems (webhook, Kafka, etc.)

## SDK (bridgeclient)

- [x] Configurable retry policy with exponential backoff
- [x] GoDoc examples (`example_test.go`)
- [ ] SDK-level context propagation (trace IDs)
- [ ] Example integration test with real bridge daemon

## Integration

- [ ] Feature flag for gradual rollout (in-process fallback)
- [x] Docker Compose setup for integration testing
- [x] `make dev-setup` one-command dev environment with all certs

## Testing

- [x] Integration tests: full gRPC round-trip (start → input → events → stop)
- [x] Integration tests: mTLS rejection (bad cert, expired, wrong CA)
- [x] Integration tests: JWT rejection (expired, wrong audience, wrong issuer)
- [x] Unit tests: subscriber reconnect/replay with ack_seq (subscribermgr_test.go)
- [ ] Load testing: concurrent sessions and event throughput

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
