# Node.js SDK Reference

> Machine-readable summary: Package `@ai-agent-bridge/client-node`. Provides three integration points: (1) `createNextJsBridgeRoute` — drop-in Next.js Pages Router API route; (2) `createBridgeWebSocketHandler` — generic Node.js WebSocket handler; (3) `useBridgeSession` React hook (import from `@ai-agent-bridge/client-node/react`). Communication protocol: WebSocket JSON (not gRPC directly). The Node.js process connects to the bridge daemon via gRPC; the browser speaks JSON over WebSocket.

---

## Architecture

```
Browser / React App
    ↕ WebSocket (JSON protocol)
Node.js server (Next.js, Express, etc.)
    ↕ gRPC (mTLS + JWT)
ai-agent-bridge daemon
    ↕ PTY
AI Agent process
```

Browsers cannot speak gRPC directly. The `@ai-agent-bridge/client-node` package runs in Node.js and translates between the browser's WebSocket JSON messages and the daemon's gRPC API.

---

## Installation

```bash
npm install @ai-agent-bridge/client-node
```

---

## Next.js Pages Router (quickest path)

Create `pages/api/bridge.ts`:

```ts
import { createNextJsBridgeRoute } from "@ai-agent-bridge/client-node";

export default createNextJsBridgeRoute({
  bridgeAddr: "localhost:9445",
  // Optional mTLS:
  // tls: {
  //   caBundlePath: "certs/ca-bundle.crt",
  //   certPath:     "certs/client.crt",
  //   keyPath:      "certs/client.key",
  //   serverName:   "bridge.local",
  // },
  // Optional JWT:
  // jwt: {
  //   privateKeyPath: "certs/jwt-signing.key",
  //   issuer:         "my-service",
  //   audience:       "bridge",
  // },
});

export const config = { api: { bodyParser: false } };
```

That's it. The route upgrades incoming HTTP connections to WebSocket and handles the full JSON protocol.

---

## Next.js App Router (custom server)

App Router does not support WebSocket upgrades in route handlers. Use a custom server instead:

```ts
// server.ts
import { createServer } from "http";
import next from "next";
import { createBridgeWebSocketHandler } from "@ai-agent-bridge/client-node";

const app = next({ dev: process.env.NODE_ENV !== "production" });
const handle = app.getRequestHandler();

app.prepare().then(() => {
  const wsHandler = createBridgeWebSocketHandler({ bridgeAddr: "localhost:9445" });

  const server = createServer((req, res) => {
    handle(req, res);
  });

  server.on("upgrade", (req, socket, head) => {
    if (req.url === "/api/bridge") {
      wsHandler.handleUpgrade(req, socket, head);
    } else {
      socket.destroy();
    }
  });

  server.listen(3000);
});
```

---

## Generic Node.js / Express

```ts
import { createServer } from "http";
import express from "express";
import { createBridgeWebSocketHandler } from "@ai-agent-bridge/client-node";

const app = express();
const server = createServer(app);
const wsHandler = createBridgeWebSocketHandler({ bridgeAddr: "localhost:9445" });

server.on("upgrade", (req, socket, head) => {
  wsHandler.handleUpgrade(req, socket, head);
});

server.listen(8080);
```

---

## React Hook

```tsx
import { useBridgeSession } from "@ai-agent-bridge/client-node/react";

function AgentPanel() {
  const bridge = useBridgeSession("ws://localhost:3000/api/bridge");

  const handleStart = () => {
    bridge.startSession({
      projectId: "my-project",
      repoPath:  "/repos/my-app",
      provider:  "claude",
    });
  };

  const handleInput = (text: string) => {
    bridge.sendInput(text);
  };

  return (
    <div>
      <p>Status: {bridge.status}</p>
      <button onClick={handleStart}>Start Agent</button>
      <div>
        {bridge.events.map(ev => (
          <span key={ev.seq}>{ev.text}</span>
        ))}
      </div>
    </div>
  );
}
```

### Hook return value

| Property | Type | Description |
|----------|------|-------------|
| `status` | string | `"idle"`, `"starting"`, `"running"`, `"attached"`, `"stopping"`, `"stopped"`, `"failed"` |
| `events` | `BridgeEvent[]` | Accumulated output events |
| `sessionId` | string \| null | Current session ID |
| `startSession(opts)` | function | Start a new session |
| `stopSession(force?)` | function | Stop the current session |
| `sendInput(text)` | function | Send text to the agent |
| `streamEvents(afterSeq?)` | function | (Re)subscribe to the event stream |
| `listSessions()` | function | Fetch sessions for the current project |
| `health()` | function | Check daemon health |

### `BridgeEvent` shape

```ts
interface BridgeEvent {
  seq:       number;   // monotonic sequence number
  eventType: string;   // e.g. "stdout", "agent_ready", "response_complete"
  text:      string;   // decoded PTY output text
  done:      boolean;  // true when the response is complete
  replay:    boolean;  // true if this event is from buffer replay
}
```

---

## WebSocket JSON Protocol

The wire protocol between the browser and the Node.js adapter is JSON over WebSocket. You can implement your own browser client against this protocol.

### Client → Server messages

#### `start_session`

```json
{
  "type": "start_session",
  "projectId": "my-project",
  "sessionId": "uuid-...",
  "repoPath": "/repos/my-app",
  "provider": "claude",
  "agentOpts": {},
  "cols": 220,
  "rows": 50
}
```

#### `stop_session`

```json
{ "type": "stop_session", "sessionId": "uuid-...", "force": false }
```

#### `write_input`

```json
{ "type": "write_input", "sessionId": "uuid-...", "clientId": "...", "data": "aGVsbG8K" }
```

`data` is base64-encoded bytes.

#### `attach_session`

```json
{
  "type": "attach_session",
  "sessionId": "uuid-...",
  "clientId": "...",
  "afterSeq": 0
}
```

#### `resize_session`

```json
{ "type": "resize_session", "sessionId": "uuid-...", "clientId": "...", "cols": 240, "rows": 60 }
```

#### `get_session`

```json
{ "type": "get_session", "sessionId": "uuid-..." }
```

#### `list_sessions`

```json
{ "type": "list_sessions", "projectId": "my-project" }
```

#### `health`

```json
{ "type": "health" }
```

#### `list_providers`

```json
{ "type": "list_providers" }
```

### Server → Client messages

#### `session_started`

```json
{
  "type": "session_started",
  "sessionId": "uuid-...",
  "status": "starting",
  "createdAt": "2026-01-01T00:00:00Z"
}
```

#### `event`

```json
{
  "type": "event",
  "seq": 42,
  "sessionId": "uuid-...",
  "eventType": "output",
  "payload": "aGVsbG8K",
  "replay": false
}
```

`payload` is base64-encoded raw PTY bytes.

#### `session_stopped`

```json
{ "type": "session_stopped", "sessionId": "uuid-...", "status": "stopped" }
```

#### `session_info`

```json
{
  "type": "session_info",
  "session": {
    "sessionId": "uuid-...",
    "projectId": "my-project",
    "provider": "claude",
    "status": "running",
    "createdAt": "2026-01-01T00:00:00Z"
  }
}
```

#### `sessions_list`

```json
{
  "type": "sessions_list",
  "sessions": [ ... ]
}
```

#### `health_response`

```json
{
  "type": "health_response",
  "status": "ok",
  "providers": [
    { "provider": "claude", "available": true }
  ]
}
```

#### `providers_list`

```json
{
  "type": "providers_list",
  "providers": [
    { "provider": "claude", "available": true, "binary": "./node_modules/.bin/claude" }
  ]
}
```

#### `error`

```json
{ "type": "error", "code": "start_session_error", "message": "session limit reached" }
```

---

## Authentication

Pass TLS and JWT options to the handler factory:

```ts
createNextJsBridgeRoute({
  bridgeAddr: "bridge.internal:9445",
  tls: {
    caBundlePath: "/etc/bridge/ca-bundle.crt",
    certPath:     "/etc/bridge/client.crt",
    keyPath:      "/etc/bridge/client.key",
    serverName:   "bridge.local",
  },
  jwt: {
    privateKeyPath: "/etc/bridge/jwt-signing.key",
    issuer:         "my-app",
    audience:       "bridge",
  },
});
```

---

## Related

- [Go SDK reference](go-sdk.md)
- [gRPC API reference](grpc-api.md)
- [Go WebSocket integration](go-websocket-integration.md)
- [packages/bridge-client-node/README.md](../packages/bridge-client-node/README.md)
