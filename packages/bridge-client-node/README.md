# @ai-agent-bridge/client-node

Node.js bridge client for the [ai-agent-bridge](https://github.com/markcallen/ai-agent-bridge) daemon.

This package provides:

- **`BridgeGrpcClient`** — Node.js gRPC client that mirrors the Go `pkg/bridgeclient` API.
- **`createBridgeWebSocketHandler`** — WebSocket server factory that bridges browser clients to the gRPC daemon using the JSON protocol defined below.
- **`createNextJsBridgeRoute`** — Drop-in Pages Router API route helper.
- **`useBridgeSession`** (React hook) — Browser-side hook with auto-reconnect and event streaming.

## Architecture

```
React App (Browser)
    ↕ WebSocket (JSON protocol)
Next.js or Go HTTP server   ← this package
    ↕ gRPC (plain or mTLS+JWT)
ai-agent-bridge daemon
```

## Installation

```bash
npm install @ai-agent-bridge/client-node
# peer dep for React hook:
npm install react
```

## Quick Start

### 1. Next.js Pages Router (API route)

Create `pages/api/bridge.ts`:

```ts
import { createNextJsBridgeRoute } from "@ai-agent-bridge/client-node";

export default createNextJsBridgeRoute({
  bridgeAddr: process.env.BRIDGE_ADDR ?? "localhost:50051",
});

export const config = { api: { bodyParser: false } };
```

### 2. React hook

```tsx
import { useBridgeSession } from "@ai-agent-bridge/client-node/react";

export function AgentPanel() {
  const bridge = useBridgeSession("ws://localhost:3000/api/bridge");

  const start = () => {
    bridge.startSession({
      projectId: "my-project",
      repoPath: "/repos/my-app",
      provider: "claude",
    });
  };

  const handleStart = () => {
    start();
    // Stream events as soon as we have a session ID
    // (listen for sessionId state to be set, then call streamEvents)
  };

  return (
    <div>
      <p>Status: {bridge.status}</p>
      <button onClick={handleStart}>Start Session</button>
      <button
        onClick={() =>
          bridge.sendInput({
            sessionId: bridge.sessionId!,
            text: "hello",
          })
        }
        disabled={!bridge.sessionId}
      >
        Send Input
      </button>
      <div>
        {bridge.events.map((ev) => (
          <p key={ev.seq}>{ev.text}</p>
        ))}
      </div>
    </div>
  );
}
```

### 3. Using the gRPC client directly (Node.js)

```ts
import { BridgeGrpcClient } from "@ai-agent-bridge/client-node";

const client = new BridgeGrpcClient({ bridgeAddr: "localhost:50051" });

// Start a session
const session = await client.startSession({
  projectId: "my-project",
  repoPath: "/repos/my-app",
  provider: "claude",
});

// Stream events
const ac = new AbortController();
for await (const event of client.streamEvents({
  sessionId: session.sessionId,
  signal: ac.signal,
})) {
  console.log(event.eventType, event.text);
  if (event.done) ac.abort();
}

client.close();
```

### 4. Next.js App Router (custom server)

For App Router projects that need WebSocket support, use a custom `server.ts`:

```ts
import { createServer } from "http";
import next from "next";
import { createBridgeWebSocketHandler } from "@ai-agent-bridge/client-node";

const dev = process.env.NODE_ENV !== "production";
const app = next({ dev });
const handle = app.getRequestHandler();

const wss = createBridgeWebSocketHandler({
  bridgeAddr: process.env.BRIDGE_ADDR ?? "localhost:50051",
});

app.prepare().then(() => {
  const server = createServer((req, res) => handle(req, res));

  server.on("upgrade", (req, socket, head) => {
    if (req.url === "/bridge") {
      wss.handleUpgrade(req, socket, head, (ws) =>
        wss.emit("connection", ws, req)
      );
    } else {
      socket.destroy();
    }
  });

  server.listen(3000, () => {
    console.log("> Ready on http://localhost:3000");
  });
});
```

Run with: `tsx server.ts`

## WebSocket JSON Protocol

All messages are JSON-encoded. The same protocol is supported by both the Node.js and Go integrations.

### Client → Server

| `type` | Fields | Description |
|--------|--------|-------------|
| `start_session` | `projectId`, `repoPath`, `provider`, `sessionId?`, `agentOpts?` | Start a new session |
| `send_input` | `sessionId`, `text`, `idempotencyKey?` | Send text input |
| `stop_session` | `sessionId`, `force?` | Stop a session |
| `stream_events` | `sessionId`, `afterSeq?`, `subscriberId?` | Subscribe to events |
| `list_sessions` | `projectId?` | List sessions |
| `get_session` | `sessionId` | Get session info |
| `health` | — | Check daemon health |
| `list_providers` | — | List available providers |

### Server → Client

| `type` | Fields | Description |
|--------|--------|-------------|
| `session_started` | `sessionId`, `status`, `createdAt` | Session created |
| `event` | `seq`, `sessionId`, `eventType`, `stream`, `text`, `done`, `error` | Streamed event |
| `input_accepted` | `accepted`, `seq` | Input acknowledgement |
| `session_stopped` | `sessionId`, `status` | Session stopped |
| `sessions_list` | `sessions[]` | List of sessions |
| `session_info` | `session` | Single session info |
| `health_response` | `status`, `providers[]` | Health info |
| `providers_list` | `providers[]` | Provider list |
| `error` | `code`, `message` | Error response |

`eventType` values mirror the proto `EventType` enum (lowercased): `session_started`, `session_stopped`, `session_failed`, `stdout`, `stderr`, `input_received`, `buffer_overflow`, `agent_ready`, `response_complete`.

## Authentication

Auth is intentionally **not built into this package** — it's handled by the consuming application. Pass auth context through the optional `metadata` option:

```ts
// Static bearer token
createBridgeWebSocketHandler({
  bridgeAddr: "localhost:50051",
  metadata: { authorization: `Bearer ${token}` },
});

// Or per-connection via middleware that extracts the token from the WS handshake
// (see the Go integration doc for mTLS + JWT examples)
```

## Go HTTP Integration

See [`docs/go-websocket-integration.md`](../../docs/go-websocket-integration.md) for instructions on exposing the same WebSocket protocol from a Go HTTP server using `pkg/bridgeclient`.

## Development

```bash
cd packages/bridge-client-node
npm install
npm run build
```

The proto file is loaded dynamically at runtime from `../../proto/bridge/v1/bridge.proto` relative to this package directory. Ensure the path exists when running from a built distribution.
