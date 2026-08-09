# Documentation Index

This is the canonical documentation index for the repository.

## Core Guides

- [install-ubuntu.md](install-ubuntu.md): apt repository installation, package contents, systemd behavior, and runtime prerequisites.
- [service.md](service.md): daemon architecture, configuration, security model, Docker usage, and operational details.
- [ai-desktops-step-ca-clients.md](ai-desktops-step-ca-clients.md): ai-desktops workflow for preloading Step CA client JWT public keys at server startup.
- [grpc-api.md](grpc-api.md): protobuf RPC surface, message fields, error codes, and client generation details.
- [go-sdk.md](go-sdk.md): Go SDK usage, client options, reconnect behavior, and API examples.

## Recommended Reading Order

1. [install-ubuntu.md](install-ubuntu.md) if you are installing the packaged daemon on Ubuntu
2. [service.md](service.md)
3. [grpc-api.md](grpc-api.md)
4. [go-sdk.md](go-sdk.md) for Go SDK integration

## Local Development

- Use the version in [`.nvmrc`](../.nvmrc) for Node.js-based tooling.
- Load API keys through `env-secrets`, not `.env` files. The repo's `make` targets automatically use `env-secrets aws` when `ENV_SECRETS_AWS_SECRET` is set.
- Run `make dev-setup` for certificates and local agent binaries.
- Run `make smoke` to validate the Dockerized bridge startup path.
- Run `make smoke-apt-local` to validate the Debian package and apt repository path locally.
