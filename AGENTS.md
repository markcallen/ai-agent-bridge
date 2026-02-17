# Repository Guidelines

## Project Structure & Module Organization
Core binaries live in `cmd/`: `cmd/bridge` (daemon) and `cmd/bridge-ca` (certificate tooling). Service contracts are in `proto/bridge/v1`, with generated Go stubs in `gen/bridge/v1` (regenerate, do not hand-edit). Runtime internals are organized under `internal/` by domain: `auth`, `bridge`, `config`, `pki`, `provider`, and `server`. Public SDK code is in `pkg/bridgeclient`. Supporting artifacts live in `config/`, `scripts/`, `certs/`, and integration scenarios in `e2e/`.

## Build, Test, and Development Commands
- `make build`: Generates protobuf stubs, then builds `bin/bridge` and `bin/bridge-ca`.
- `make proto`: Regenerates Go code from `proto/bridge/v1/bridge.proto`.
- `make test`: Runs all Go tests with race detection (`go test -race -count=1 ./...`).
- `make test-cover`: Produces `coverage.out` and `coverage.html`.
- `make lint`: Runs `golangci-lint` across the module.
- `make fmt`: Applies `gofmt -s -w .` and `goimports -w .`.
- `make dev-setup`: Builds binaries and generates development certificates.

## Coding Style & Naming Conventions
Use standard Go formatting and imports (`make fmt` before commits). Keep packages focused and lower-case; exported identifiers use `CamelCase`, unexported use `camelCase`. Prefer descriptive file names aligned to domain behavior (for example, `supervisor.go`, `interceptors.go`). Do not edit generated files in `gen/`; update `proto/` and rerun `make proto`.

## Testing Guidelines
Write table-driven unit tests beside implementation files with `_test.go` suffix (for example, `internal/bridge/eventbuf_test.go`). Favor deterministic tests and include race-safe behavior checks for concurrent code paths. Run `make test` locally before opening a PR; use `make test-cover` when changing critical auth, session, or provider flows.

## Commit & Pull Request Guidelines
History follows concise, imperative subjects (for example, `Add gRPC server...`, `Fix data races...`). Keep commits scoped to a single logical change and include regenerated artifacts when proto changes require them. PRs should include:
- A short problem/solution summary.
- Linked issue or task reference when available.
- Test evidence (`make test`, and e2e notes when relevant).
- Config or operational impact (ports, certs, auth behavior).

## Security & Configuration Tips
Treat `config/bridge.yaml` as local-dev plaintext mode only. For realistic environments, use `config/bridge-dev.yaml` with mTLS and JWT keys from `certs/`. Never commit private keys, tokens, or environment-specific secrets.
