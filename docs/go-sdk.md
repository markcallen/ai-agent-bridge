# Go SDK Reference

> Machine-readable summary: Package `github.com/markcallen/ai-agent-bridge/pkg/bridgeclient`. Constructor: `bridgeclient.New(...Option)`. Key methods: `StartSession`, `StopSession`, `GetSession`, `ListSessions`, `AttachSession` (returns `*OutputStream`), `WriteInput`, `ResizeSession`, `Health`, `ListProviders`. Proto types in `github.com/markcallen/ai-agent-bridge/gen/bridge/v1`.

---

## Installation

```bash
go get github.com/markcallen/ai-agent-bridge/pkg/bridgeclient
```

The generated protobuf types are a transitive dependency — you only need to import them directly if you construct request objects:

```bash
go get github.com/markcallen/ai-agent-bridge/gen/bridge/v1
```

---

## Connecting

### Plain (no TLS) — local dev only

```go
import "github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"

client, err := bridgeclient.New(
    bridgeclient.WithTarget("127.0.0.1:9445"),
)
if err != nil {
    log.Fatal(err)
}
defer client.Close()
```

### mTLS only

```go
client, err := bridgeclient.New(
    bridgeclient.WithTarget("bridge.internal:9445"),
    bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
        CABundlePath: "certs/ca-bundle.crt",
        CertPath:     "certs/client.crt",
        KeyPath:      "certs/client.key",
        ServerName:   "bridge.local",
    }),
)
```

### mTLS + JWT (production)

```go
client, err := bridgeclient.New(
    bridgeclient.WithTarget("bridge.internal:9445"),
    bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
        CABundlePath: "certs/ca-bundle.crt",
        CertPath:     "certs/client.crt",
        KeyPath:      "certs/client.key",
        ServerName:   "bridge.local",
    }),
    bridgeclient.WithJWT(bridgeclient.JWTConfig{
        PrivateKeyPath: "certs/jwt-signing.key",
        Issuer:         "my-service",
        Audience:       "bridge",
        TTL:            5 * time.Minute,
    }),
)
```

JWTs are minted per-RPC automatically. The `project_id` from the first `StartSession` call is embedded in subsequent tokens; call `client.SetProject(id)` to override it.

### Options reference

| Option | Description |
|--------|-------------|
| `WithTarget(addr)` | gRPC endpoint address (required) |
| `WithMTLS(MTLSConfig)` | Enable mTLS transport |
| `WithJWT(JWTConfig)` | Enable per-RPC JWT authentication |
| `WithTimeout(d)` | Per-RPC deadline (default: 30s) |
| `WithRetry(RetryConfig)` | Retry policy for transient errors |
| `WithCursorStore(CursorStore)` | Custom cursor persistence for reconnect tracking |

---

## Session Management

```go
import bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"

ctx := context.Background()

// Start
resp, err := client.StartSession(ctx, &bridgev1.StartSessionRequest{
    ProjectId: "my-project",
    SessionId: "session-001",      // must be a UUID
    RepoPath:  "/repos/my-app",
    Provider:  "claude",
    Cols:      220,
    Rows:      50,
})

// Query
info, err := client.GetSession(ctx, &bridgev1.GetSessionRequest{
    SessionId: "session-001",
})
fmt.Println(info.Status)

// List
list, err := client.ListSessions(ctx, &bridgev1.ListSessionsRequest{
    ProjectId: "my-project",
})

// Stop
_, err = client.StopSession(ctx, &bridgev1.StopSessionRequest{
    SessionId: "session-001",
    Force:     false,
})
```

---

## Streaming Output

`AttachSession` returns an `*OutputStream`. Call `RecvAll` to receive events via a callback.

```go
stream, err := client.AttachSession(ctx, &bridgev1.AttachSessionRequest{
    SessionId: "session-001",
    // ClientId: "stable-id",   // omit to auto-generate
    // AfterSeq: lastSeq,       // omit to replay all retained output
})
if err != nil {
    log.Fatal(err)
}

err = stream.RecvAll(ctx, func(ev *bridgev1.AttachSessionEvent) error {
    switch ev.Type {
    case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ATTACHED:
        fmt.Printf("attached, buffer seq %d–%d\n", ev.OldestSeq, ev.LastSeq)
    case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_OUTPUT:
        os.Stdout.Write(ev.Payload)
    case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_REPLAY_GAP:
        fmt.Fprintln(os.Stderr, "replay gap:", ev.Error)
    case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_SESSION_EXIT:
        fmt.Fprintf(os.Stderr, "agent exited with code %d\n", ev.ExitCode)
        return io.EOF  // stop the stream
    case bridgev1.AttachEventType_ATTACH_EVENT_TYPE_ERROR:
        return fmt.Errorf("stream error: %s", ev.Error)
    }
    return nil
})
```

### Reconnect with cursor tracking

The SDK tracks the last received sequence number via a `CursorStore`. On reconnect, pass `AfterSeq: 0` (or omit it) — the SDK will automatically resume from where it left off:

```go
// First connection — stream picks up from seq 0 (full replay)
stream1, _ := client.AttachSession(ctx, &bridgev1.AttachSessionRequest{
    SessionId: "session-001",
    ClientId:  "my-client",   // stable across reconnects
})
stream1.RecvAll(ctx, handler)

// Later reconnect — SDK restores the cursor automatically
stream2, _ := client.AttachSession(ctx, &bridgev1.AttachSessionRequest{
    SessionId: "session-001",
    ClientId:  "my-client",   // same client ID
})
stream2.RecvAll(ctx, handler)  // resumes from last processed seq
```

Use `WithCursorStore` to plug in a persistent store (Redis, database) for durable cursor tracking across process restarts.

---

## Sending Input

```go
resp, err := client.WriteInput(ctx, &bridgev1.WriteInputRequest{
    SessionId: "session-001",
    ClientId:  stream.ClientID(),  // must match the attached client ID
    Data:      []byte("hello\n"),
})
if !resp.Accepted {
    fmt.Println("input not accepted — is a client attached?")
}
```

---

## Resizing the PTY

```go
_, err = client.ResizeSession(ctx, &bridgev1.ResizeSessionRequest{
    SessionId: "session-001",
    ClientId:  stream.ClientID(),
    Cols:      240,
    Rows:      60,
})
```

---

## Health and Providers

```go
health, err := client.Health(ctx)
fmt.Println(health.Status)  // "ok" or "degraded"
for _, p := range health.Providers {
    fmt.Printf("%s: available=%v\n", p.Provider, p.Available)
}

providers, err := client.ListProviders(ctx)
for _, p := range providers.Providers {
    fmt.Printf("%s binary=%s\n", p.Provider, p.Binary)
}
```

---

## Full Working Example

See [`examples/chat/main.go`](../examples/chat/main.go) for a complete interactive PTY example that:

- Parses flags for target, provider, TLS certs, and JWT keys
- Starts a session and attaches in raw terminal mode
- Forwards SIGWINCH resize events
- Streams stdout to the local terminal
- Sends stdin keystrokes to the remote PTY

Run it with `make chat-claude` (or `chat-opencode`, `chat-codex`, `chat-gemini`).

---

## CursorStore Interface

The SDK ships with `NewMemoryCursorStore()` (in-process, no persistence). Implement the `CursorStore` interface for durable storage:

```go
type CursorStore interface {
    SaveCursor(ctx context.Context, sessionID, clientID string, seq uint64) error
    LoadCursor(ctx context.Context, sessionID, clientID string) (uint64, error)
}
```

---

## Related

- [gRPC API reference](grpc-api.md) — Proto types and field semantics
- [Service reference](service.md) — Daemon configuration
- [Node.js SDK](node-sdk.md) — Browser and Next.js integration
- [Go WebSocket integration](go-websocket-integration.md) — Expose the bridge via WebSocket from a Go HTTP server
