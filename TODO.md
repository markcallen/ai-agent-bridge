# TODO

Single source of remaining work, reconciled from the old `TODO.md`, `tasks/todo.md`, and `tasks/update-plan.md`.

Completed items from the PTY transport cutover and persistence phases were removed from this file. This file keeps only open work.

## Product Work Aligned With `PRD.md`

### Security

- [ ] OCSP stapling / live certificate revocation support
- [ ] SPIFFE/SPIRE integration for workload identity
- [ ] Policy-as-code (OPA/Rego) for authorization decisions

### Providers

- [ ] `droid` provider adapter

### Persistence, Replay, and Scaling

- [ ] Multi-bridge clustering and session migration

### SDKs and Distribution

- [ ] Python SDK (generated from protobuf plus thin wrapper)
- [ ] TypeScript SDK (generated from protobuf)

### Deployment and Operations

- [ ] Web UI for bridge status and session management

## Additional Open Work Not Required By `PRD.md`

### Providers and Extensibility

- [ ] Plugin system for custom provider adapters

### Storage and Integrations

- [ ] Event export to external systems (webhook, Kafka, etc.)
- [ ] Full live reattach after daemon restart; current restart recovery is replay-only

### Go SDK

- [ ] SDK-level context propagation (trace IDs)
- [ ] Example integration test with a real bridge daemon
- [ ] Publish protobuf definitions as a standalone package

### Rollout and Testing

- [ ] Feature flag for gradual rollout / in-process fallback
- [ ] Load testing for concurrent sessions and event throughput
- [ ] Migrate e2e tests to `testify/suite` (issue #2)
- [ ] Systemd unit file and deployment packaging
- [ ] Helm chart for Kubernetes deployment

### Repository Rule-Alignment Follow-Ups

Verified open gaps:

- [ ] Remove remaining `.env`-centric local-dev guidance from docs/examples and keep `env-secrets` as the documented secret source of truth
- [ ] Confirm and document that automated Go coverage is at or above 75% for the required scope
- [ ] Finish docs alignment for quick-start, local-dev, release, troubleshooting, and architecture paths
- [ ] Re-run full verification and capture evidence for the remaining rule-alignment work

Needs targeted audit before it should stay in TODO:

- [ ] Review structured logging fields and secret-redaction coverage across Go and TypeScript paths, then either close the gap or remove this item if current behavior is already compliant
