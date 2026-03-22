# Go HTTP WebSocket Integration

This guide shows how to expose the ai-agent-bridge WebSocket JSON protocol from a Go HTTP server using `pkg/bridgeclient`.

The same JSON protocol is understood by the `useBridgeSession` React hook and the `@ai-agent-bridge/client-node` package.

## Architecture

```
React App (Browser)
    ↕ WebSocket (JSON protocol)
Go HTTP server   ← what this guide covers
    ↕ gRPC (plain or mTLS+JWT)
ai-agent-bridge daemon
```

---

## 1. Dependencies

Add `github.com/gorilla/websocket` to your `go.mod`:

```bash
go get github.com/gorilla/websocket
```

---

## 2. Message Structs

Define Go structs that match the JSON protocol.

```go
package bridgews

import "encoding/json"

// ClientMessage is a tagged union; the Type field determines the payload.
type ClientMessage struct {
    Type string `json:"type"`

    // start_session
    ProjectID  string            `json:"projectId,omitempty"`
    SessionID  string            `json:"sessionId,omitempty"`
    RepoPath   string            `json:"repoPath,omitempty"`
    Provider   string            `json:"provider,omitempty"`
    AgentOpts  map[string]string `json:"agentOpts,omitempty"`

    // send_input
    Text           string `json:"text,omitempty"`
    IdempotencyKey string `json:"idempotencyKey,omitempty"`

    // stop_session
    Force bool `json:"force,omitempty"`

    // stream_events
    AfterSeq     uint64 `json:"afterSeq,omitempty"`
    SubscriberID string `json:"subscriberId,omitempty"`

    // list_sessions — uses ProjectID above
}

// ServerMessage is a tagged union returned to the client.
type ServerMessage struct {
    Type string `json:"type"`

    // session_started
    Status    string `json:"status,omitempty"`
    CreatedAt string `json:"createdAt,omitempty"`

    // event
    Seq       uint64 `json:"seq,omitempty"`
    EventType string `json:"eventType,omitempty"`
    Stream    string `json:"stream,omitempty"`
    Done      bool   `json:"done,omitempty"`

    // input_accepted
    Accepted bool `json:"accepted,omitempty"`

    // sessions_list / session_info
    Sessions []SessionInfo `json:"sessions,omitempty"`
    Session  *SessionInfo  `json:"session,omitempty"`

    // health_response — "status" matches the shared protocol (not "healthStatus")
    HealthStatus string           `json:"status,omitempty"`
    Providers    []ProviderHealth `json:"providers,omitempty"`

    // providers_list — "providers" matches the shared protocol (not "providerList")
    ProviderList []ProviderInfo `json:"providers,omitempty"`

    // shared
    Text    string `json:"text,omitempty"`
    Error   string `json:"error,omitempty"`
    Code    string `json:"code,omitempty"`
    Message string `json:"message,omitempty"`
}

// SessionInfo mirrors pkg/bridgeclient's session data.
type SessionInfo struct {
    SessionID string `json:"sessionId"`
    ProjectID string `json:"projectId"`
    Provider  string `json:"provider"`
    Status    string `json:"status"`
    CreatedAt string `json:"createdAt"`
    StoppedAt string `json:"stoppedAt,omitempty"`
    Error     string `json:"error,omitempty"`
}

type ProviderHealth struct {
    Provider  string `json:"provider"`
    Available bool   `json:"available"`
    Error     string `json:"error,omitempty"`
}

type ProviderInfo struct {
    Provider  string `json:"provider"`
    Available bool   `json:"available"`
    Binary    string `json:"binary"`
    Version   string `json:"version"`
}

func errMsg(code, message string) ServerMessage {
    return ServerMessage{Type: "error", Code: code, Message: message}
}

func sessionStatusString(s int32) string {
    statuses := map[int32]string{
        0: "unspecified", 1: "starting", 2: "running",
        3: "stopping", 4: "stopped", 5: "failed",
    }
    if name, ok := statuses[s]; ok {
        return name
    }
    return "unspecified"
}

func eventTypeString(t int32) string {
    types := map[int32]string{
        0: "unspecified", 1: "session_started", 2: "session_stopped",
        3: "session_failed", 4: "stdout", 5: "stderr",
        6: "input_received", 7: "buffer_overflow",
        8: "agent_ready", 9: "response_complete",
    }
    if name, ok := types[t]; ok {
        return name
    }
    return "unspecified"
}

// MarshalJSON encodes a ServerMessage as JSON bytes.
func (m ServerMessage) MarshalJSON() ([]byte, error) {
    type Alias ServerMessage
    return json.Marshal(Alias(m))
}
```

---

## 3. Handler

```go
package bridgews

import (
    "context"
    "encoding/json"
    "log/slog"
    "net/http"
    "sync"
    "time"

    "github.com/gorilla/websocket"
    "github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
    bridgev1 "github.com/markcallen/ai-agent-bridge/gen/bridge/v1"
)

var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool {
        // TODO: restrict to known origins in production
        return true
    },
}

// Handler wraps a bridgeclient.Client and serves the bridge WebSocket protocol.
type Handler struct {
    client *bridgeclient.Client
    logger *slog.Logger
}

// NewHandler creates a BridgeWebSocketHandler from an existing bridgeclient.Client.
func NewHandler(client *bridgeclient.Client, logger *slog.Logger) *Handler {
    if logger == nil {
        logger = slog.Default()
    }
    return &Handler{client: client, logger: logger}
}

// ServeHTTP handles a single WebSocket connection.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        h.logger.Error("WebSocket upgrade failed", "error", err)
        return
    }
    defer conn.Close()

    ctx, cancel := context.WithCancel(r.Context())
    defer cancel()

    var (
        mu            sync.Mutex
        activeStreams  = map[string]context.CancelFunc{}
    )

    send := func(msg ServerMessage) {
        data, _ := json.Marshal(msg)
        mu.Lock()
        defer mu.Unlock()
        if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
            h.logger.Warn("WebSocket write error", "error", err)
        }
    }

    sendErr := func(code, message string) {
        send(errMsg(code, message))
    }

    for {
        _, rawMsg, err := conn.ReadMessage()
        if err != nil {
            if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
                h.logger.Warn("WebSocket closed unexpectedly", "error", err)
            }
            break
        }

        var msg ClientMessage
        if err := json.Unmarshal(rawMsg, &msg); err != nil {
            sendErr("parse_error", "Invalid JSON")
            continue
        }

        switch msg.Type {
        case "start_session":
            resp, err := h.client.StartSession(ctx, &bridgev1.StartSessionRequest{
                ProjectId: msg.ProjectID,
                SessionId: msg.SessionID,
                RepoPath:  msg.RepoPath,
                Provider:  msg.Provider,
                AgentOpts: msg.AgentOpts,
            })
            if err != nil {
                sendErr("start_session_error", err.Error())
                continue
            }
            var createdAt string
            if resp.CreatedAt != nil {
                createdAt = time.Unix(resp.CreatedAt.Seconds, int64(resp.CreatedAt.Nanos)).UTC().Format(time.RFC3339Nano)
            }
            send(ServerMessage{
                Type:      "session_started",
                SessionID: resp.SessionId,
                Status:    sessionStatusString(int32(resp.Status)),
                CreatedAt: createdAt,
            })

        case "send_input":
            resp, err := h.client.SendInput(ctx, &bridgev1.SendInputRequest{
                SessionId:      msg.SessionID,
                Text:           msg.Text,
                IdempotencyKey: msg.IdempotencyKey,
            })
            if err != nil {
                sendErr("send_input_error", err.Error())
                continue
            }
            send(ServerMessage{
                Type:     "input_accepted",
                Accepted: resp.Accepted,
                Seq:      resp.Seq,
            })

        case "stop_session":
            resp, err := h.client.StopSession(ctx, &bridgev1.StopSessionRequest{
                SessionId: msg.SessionID,
                Force:     msg.Force,
            })
            if err != nil {
                sendErr("stop_session_error", err.Error())
                continue
            }
            send(ServerMessage{
                Type:      "session_stopped",
                SessionID: msg.SessionID,
                Status:    sessionStatusString(int32(resp.Status)),
            })

        case "stream_events":
            sessionID := msg.SessionID

            // Cancel any existing stream for this session
            if cancel, ok := activeStreams[sessionID]; ok {
                cancel()
            }
            streamCtx, streamCancel := context.WithCancel(ctx)
            activeStreams[sessionID] = streamCancel

            go func() {
                defer func() {
                    streamCancel()
                    delete(activeStreams, sessionID)
                }()

                stream, err := h.client.StreamEvents(streamCtx, &bridgev1.StreamEventsRequest{
                    SessionId:    sessionID,
                    AfterSeq:     msg.AfterSeq,
                    SubscriberId: msg.SubscriberID,
                })
                if err != nil {
                    sendErr("stream_events_error", err.Error())
                    return
                }

                stream.RecvAll(streamCtx, func(ev *bridgev1.SessionEvent) error {
                    send(ServerMessage{
                        Type:      "event",
                        Seq:       ev.Seq,
                        SessionID: ev.SessionId,
                        EventType: eventTypeString(int32(ev.Type)),
                        Stream:    ev.Stream,
                        Text:      ev.Text,
                        Done:      ev.Done,
                        Error:     ev.Error,
                    })
                    return nil
                })
            }()

        case "list_sessions":
            resp, err := h.client.ListSessions(ctx, &bridgev1.ListSessionsRequest{
                ProjectId: msg.ProjectID,
            })
            if err != nil {
                sendErr("list_sessions_error", err.Error())
                continue
            }
            var sessions []SessionInfo
            for _, s := range resp.Sessions {
                sessions = append(sessions, protoToSessionInfo(s))
            }
            send(ServerMessage{Type: "sessions_list", Sessions: sessions})

        case "get_session":
            resp, err := h.client.GetSession(ctx, &bridgev1.GetSessionRequest{
                SessionId: msg.SessionID,
            })
            if err != nil {
                sendErr("get_session_error", err.Error())
                continue
            }
            info := protoToSessionInfo(resp)
            send(ServerMessage{Type: "session_info", Session: &info})

        case "health":
            resp, err := h.client.Health(ctx)
            if err != nil {
                sendErr("health_error", err.Error())
                continue
            }
            var providers []ProviderHealth
            for _, p := range resp.Providers {
                providers = append(providers, ProviderHealth{
                    Provider:  p.Provider,
                    Available: p.Available,
                    Error:     p.Error,
                })
            }
            send(ServerMessage{
                Type:         "health_response",
                HealthStatus: resp.Status, // serializes as "status" per the JSON tag
                Providers:    providers,
            })

        case "list_providers":
            resp, err := h.client.ListProviders(ctx)
            if err != nil {
                sendErr("list_providers_error", err.Error())
                continue
            }
            var providers []ProviderInfo
            for _, p := range resp.Providers {
                providers = append(providers, ProviderInfo{
                    Provider:  p.Provider,
                    Available: p.Available,
                    Binary:    p.Binary,
                    Version:   p.Version,
                })
            }
            send(ServerMessage{Type: "providers_list", ProviderList: providers}) // serializes as "providers" per the JSON tag

        default:
            sendErr("unknown_message_type", "Unknown message type: "+msg.Type)
        }
    }

    // Cancel all active streams on disconnect
    for _, cancel := range activeStreams {
        cancel()
    }
}

func protoToSessionInfo(s *bridgev1.GetSessionResponse) SessionInfo {
    info := SessionInfo{
        SessionID: s.SessionId,
        ProjectID: s.ProjectId,
        Provider:  s.Provider,
        Status:    sessionStatusString(int32(s.Status)),
        Error:     s.Error,
    }
    if s.CreatedAt != nil {
        info.CreatedAt = time.Unix(s.CreatedAt.Seconds, int64(s.CreatedAt.Nanos)).UTC().Format(time.RFC3339Nano)
    }
    if s.StoppedAt != nil {
        info.StoppedAt = time.Unix(s.StoppedAt.Seconds, int64(s.StoppedAt.Nanos)).UTC().Format(time.RFC3339Nano)
    }
    return info
}
```

---

## 4. Wiring It Up

### Standard `net/http`

```go
package main

import (
    "log"
    "net/http"

    "github.com/markcallen/ai-agent-bridge/pkg/bridgeclient"
    "your-module/bridgews"
)

func main() {
    client, err := bridgeclient.New(
        bridgeclient.WithTarget("localhost:50051"),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    handler := bridgews.NewHandler(client, nil)
    http.Handle("/bridge", handler)

    log.Println("Listening on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

### With `chi`

```go
import "github.com/go-chi/chi/v5"

r := chi.NewRouter()
r.Handle("/bridge", bridgews.NewHandler(client, nil))
http.ListenAndServe(":8080", r)
```

### With `gorilla/mux`

```go
import "github.com/gorilla/mux"

r := mux.NewRouter()
r.Handle("/bridge", bridgews.NewHandler(client, nil))
http.ListenAndServe(":8080", r)
```

---

## 5. Authentication

### JWT (per-RPC)

Pass JWT credentials when creating the bridge client:

```go
client, err := bridgeclient.New(
    bridgeclient.WithTarget("localhost:50051"),
    bridgeclient.WithJWT(bridgeclient.JWTConfig{
        PrivateKeyPath: "/etc/bridge/signing.key",
        Issuer:         "my-service",
        Audience:       "bridge",
        TTL:            5 * time.Minute,
    }),
)
```

### mTLS (transport security)

```go
client, err := bridgeclient.New(
    bridgeclient.WithTarget("bridge.internal:50051"),
    bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
        CABundlePath: "/etc/bridge/ca-bundle.pem",
        CertPath:     "/etc/bridge/client.crt",
        KeyPath:      "/etc/bridge/client.key",
        ServerName:   "bridge.internal",
    }),
)
```

### Per-Connection Auth (WebSocket → gRPC)

If you need to pass a per-user token to the bridge daemon, extract it from the WebSocket handshake request and create a per-connection bridge client:

```go
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Extract token from Authorization header or cookie
    token := r.Header.Get("X-Bridge-Token")

    // Create a per-connection client with the token as metadata
    // (or use a shared client with per-RPC metadata if you control the gRPC interceptor)
    client, _ := bridgeclient.New(
        bridgeclient.WithTarget(h.bridgeAddr),
        // ... credentials ...
    )
    defer client.Close()

    // Hand off to the connection handler
    // (you'd refactor Handler to accept a client per connection)
    _ = token
    conn, _ := upgrader.Upgrade(w, r, nil)
    _ = conn
}
```

---

## 6. CORS and Origin Checking

For production, restrict WebSocket origins in the upgrader:

```go
upgrader := websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool {
        origin := r.Header.Get("Origin")
        return origin == "https://app.example.com"
    },
}
```

---

## 7. Related

- Node.js bridge client: [`packages/bridge-client-node`](../packages/bridge-client-node/README.md)
- Go bridge client API: [`pkg/bridgeclient`](../pkg/bridgeclient/)
- gRPC proto definition: [`proto/bridge/v1/bridge.proto`](../proto/bridge/v1/bridge.proto)
