# Documentation Index

> This index is designed for AI-agent consumption. Each entry states what the document covers and when to read it.

---

## [service.md](service.md)

**Read this when:** you need to run or configure the bridge daemon, understand the session lifecycle, set up TLS certificates, tune rate limits, or understand the security model.

Covers: architecture diagram, component overview, daemon startup, Docker usage, full YAML configuration reference, `bridge-ca` certificate management, session state machine, security model, observability.

---

## [grpc-api.md](grpc-api.md)

**Read this when:** you are building a gRPC client in any language, need the exact request/response field types, want to understand event types and error codes, or are generating protobuf stubs.

Covers: all 9 RPCs (`StartSession`, `StopSession`, `GetSession`, `ListSessions`, `AttachSession`, `WriteInput`, `ResizeSession`, `Health`, `ListProviders`), all message fields, `AttachEventType` enum, `SessionStatus` enum, gRPC error codes, reconnect pattern, code generation example.

---

## [go-sdk.md](go-sdk.md)

**Read this when:** you are integrating the bridge into a Go application.

Covers: `go get` install, `bridgeclient.New` with all options, `StartSession`, `StopSession`, `GetSession`, `ListSessions`, `AttachSession` + `OutputStream.RecvAll`, reconnect with cursor tracking, `WriteInput`, `ResizeSession`, `Health`, `ListProviders`, `CursorStore` interface.

---

## [node-sdk.md](node-sdk.md)

**Read this when:** you are integrating the bridge into a Next.js, Express, or browser React application.

Covers: `npm install`, `createNextJsBridgeRoute` (Pages Router), `createBridgeWebSocketHandler` (App Router / custom server), `useBridgeSession` React hook, full WebSocket JSON protocol (all client→server and server→client message types), TLS and JWT configuration.

---

## [go-websocket-integration.md](go-websocket-integration.md)

**Read this when:** you need to expose the bridge WebSocket JSON protocol from a Go HTTP server (not Node.js).

Covers: Go struct definitions for the WebSocket JSON protocol, full WebSocket handler implementation using `bridgeclient`, wiring with `net/http`, `chi`, and `gorilla/mux`, CORS and origin checking, per-connection auth.

---

## Quick reference

| Task | Document |
|------|----------|
| Run the daemon | [service.md](service.md) |
| Configure providers | [service.md](service.md) |
| Set up TLS + JWT | [service.md](service.md) |
| Use from Go | [go-sdk.md](go-sdk.md) |
| Use from Next.js | [node-sdk.md](node-sdk.md) |
| Use from React | [node-sdk.md](node-sdk.md) |
| Expose via Go HTTP server | [go-websocket-integration.md](go-websocket-integration.md) |
| gRPC field types / error codes | [grpc-api.md](grpc-api.md) |
| Generate client in another language | [grpc-api.md](grpc-api.md) |
| Reconnect / replay buffered output | [grpc-api.md](grpc-api.md), [go-sdk.md](go-sdk.md) |
