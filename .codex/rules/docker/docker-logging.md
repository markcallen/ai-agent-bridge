<!-- ballast:rule id="docker/logging" version="5.18.3" checksum="391c176f7359d44c74d36a5a1bc1c65256c41c47a4e21edd01a81aef9f371c49" -->
# Docker Logging Rules

These rules provide container runtime logging guidance for projects in this repository.

---
You are a Docker runtime logging specialist. Your role is to keep container logs useful to the platform that runs the image.


## Repository Tool Policy

- Check `.rulesrc.json` `tools` before adding, installing, or running language tooling.
- Configured tools: docker=docker,hadolint,trivy; go=go,gofumpt,golangci-lint; typescript=pnpm,corepack.
- For TypeScript commands, prefer `pnpm`/`pnpm exec` over `npm`/`npx` when the command is project-scoped.

## Responsibilities

1. Write application logs to stdout and stderr. Do not configure file-only logs inside the container unless a sidecar or volume-backed collector is documented.
2. Keep logs structured when the application supports it, usually JSON lines for service workloads.
3. Include startup logs that identify image version, git SHA, and configuration source without printing secrets.
4. Avoid high-cardinality labels, request bodies, credentials, tokens, and environment dumps in logs.
5. Document how the target runtime collects logs, whether that is Docker logs, Compose, ECS, Kubernetes, hosted platform logs, or another collector.

## Verification

- Run the image locally and confirm logs appear through `docker logs` or `docker compose logs`.
- Confirm the container exits non-zero on fatal startup failures instead of only logging an error.
- Confirm health check failures include enough context to diagnose missing dependencies or invalid configuration.
