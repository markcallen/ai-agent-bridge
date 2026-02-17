# AI Agent Bridge

A standalone gRPC daemon and Go SDK that provides a secure, zero-trust communication layer between control-plane systems and AI agent processes. It manages AI agent subprocess lifecycles (codex, claude, opencode) and exposes a unified API for session management, command routing, and event streaming.

## Quick Start

### Prerequisites

- [Go 1.25+](https://go.dev/dl/)
- (Optional) `protoc` with `protoc-gen-go` and `protoc-gen-go-grpc` — only needed if modifying `.proto` files

### Clone and Build

```bash
git clone https://github.com/markcallen/ai-agent-bridge.git
cd ai-agent-bridge
make build
```

This produces two binaries in `bin/`:
- `bin/bridge` — the bridge daemon
- `bin/bridge-ca` — certificate authority management CLI

### Generate Dev Certificates

```bash
./scripts/dev_certs.sh
```

This creates a full set of development certificates in `certs/`:
- CA certificate and key
- Server certificate for `localhost`
- Client certificate for testing
- Trust bundle
- Ed25519 JWT signing keypair

### Create a Dev Config

Create `config/bridge-dev.yaml`:

```yaml
server:
  listen: "127.0.0.1:9445"

tls:
  ca_bundle: "certs/ca-bundle.crt"
  cert: "certs/bridge.local.crt"
  key: "certs/bridge.local.key"

auth:
  jwt_public_keys:
    - issuer: "dev"
      key_path: "certs/jwt-signing.pub"
  jwt_audience: "bridge"
  jwt_max_ttl: "5m"

sessions:
  max_per_project: 5
  max_global: 20
  idle_timeout: "30m"
  stop_grace_period: "10s"
  event_buffer_size: 10000

input:
  max_size_bytes: 65536

providers:
  claude:
    binary: "claude"
    args: ["--print", "--verbose"]
    startup_timeout: "30s"

allowed_paths:
  - "/home"
  - "/tmp"

logging:
  level: "info"
  format: "json"
```

### Run the Bridge

```bash
bin/bridge --config config/bridge-dev.yaml
```

The daemon starts on `127.0.0.1:9445` with mTLS and JWT authentication enabled.

To run **without TLS** for quick local testing (not recommended beyond initial exploration):

```bash
bin/bridge --config config/bridge.yaml
```

The default `config/bridge.yaml` has empty cert paths, so the bridge starts in plaintext dev mode with auth disabled.

### Run Tests

```bash
make test
```

## Project Structure

```
cmd/bridge/          Bridge daemon binary
cmd/bridge-ca/       CA and certificate management CLI
pkg/bridgeclient/    Go SDK for consumer integration
proto/bridge/v1/     Protobuf service definitions
gen/bridge/v1/       Generated protobuf Go code
internal/auth/       mTLS, JWT, and gRPC interceptors
internal/bridge/     Session supervisor, event buffer, provider registry, policy
internal/config/     YAML configuration loader
internal/pki/        CA management, cert issuance, cross-signing, verification
internal/provider/   Stdio-based provider adapters (codex, claude, opencode)
internal/server/     gRPC server implementation
config/              Default configuration files
scripts/             Development helper scripts
```

## Makefile Targets

| Target | Description |
|---|---|
| `make build` | Build both binaries (runs proto generation first) |
| `make test` | Run all tests with race detection |
| `make test-cover` | Run tests with coverage report |
| `make proto` | Regenerate protobuf Go code |
| `make lint` | Run golangci-lint |
| `make fmt` | Format code with gofmt and goimports |
| `make certs` | Initialize a bridge CA in `certs/` |
| `make dev-setup` | Build + generate dev certs |
| `make clean` | Remove build artifacts |

## bridge-ca Commands

```bash
bridge-ca init         # Initialize a new CA
bridge-ca issue        # Issue a server or client certificate
bridge-ca cross-sign   # Cross-sign an external CA certificate
bridge-ca bundle       # Build a trust bundle from multiple CA certs
bridge-ca jwt-keygen   # Generate Ed25519 keypair for JWT signing
bridge-ca verify       # Verify a certificate against a trust bundle
```

Run `bridge-ca <command> --help` for details on each command.

## Documentation

- [PRD.md](PRD.md) — Product requirements document
- [ARCHITECTURE.md](ARCHITECTURE.md) — System architecture and design
- [PLAN.md](PLAN.md) — Implementation plan
- [TODO.md](TODO.md) — Current task tracking
