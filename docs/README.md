# Documentation Index

This is the canonical documentation index for the repository.

## Core Guides

- [service.md](service.md): daemon architecture, configuration, security model, Docker usage, and operational details.
- [grpc-api.md](grpc-api.md): protobuf RPC surface, message fields, error codes, and client generation details.
- [go-sdk.md](go-sdk.md): Go SDK usage, client options, reconnect behavior, and API examples.
- [node-sdk.md](node-sdk.md): Node.js and React SDK usage, WebSocket bridge protocol, and Next.js integration.
- [go-websocket-integration.md](go-websocket-integration.md): embedding the bridge WebSocket protocol into a Go HTTP server.

## Recommended Reading Order

1. [service.md](service.md)
2. [grpc-api.md](grpc-api.md)
3. [go-sdk.md](go-sdk.md) or [node-sdk.md](node-sdk.md), depending on the client you are building

## Local Development

- Use the version in [`.nvmrc`](../.nvmrc) for Node.js-based tooling.
- Load API keys through `env-secrets`, not `.env` files.
- Run `make dev-setup` for certificates and local agent binaries.
- Run `make smoke` to validate the Dockerized bridge startup path.
