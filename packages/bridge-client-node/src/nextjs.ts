/**
 * Next.js integration helpers for the ai-agent-bridge WebSocket handler.
 *
 * ## Pages Router (API route)
 *
 * Create `pages/api/bridge.ts`:
 *
 * ```ts
 * import { createNextJsBridgeRoute } from "@ai-agent-bridge/client-node/nextjs";
 * export default createNextJsBridgeRoute({ bridgeAddr: "localhost:50051" });
 * export const config = { api: { bodyParser: false } };
 * ```
 *
 * ## App Router (custom server)
 *
 * See the `createNextJsCustomServer` snippet below for a custom `server.ts`
 * that attaches the WebSocket handler to a standalone Next.js HTTP server.
 */

import type { IncomingMessage, ServerResponse } from "http";
import { createBridgeWebSocketHandler } from "./websocket-handler";
import type { BridgeWebSocketHandlerOptions } from "./websocket-handler";
import type { WebSocketServer, WebSocket as WsWebSocket } from "ws";

// Extend the Node.js Server type to allow us to stash the WSS instance.
interface ExtendedServer {
  bridgeWss?: WebSocketServer;
}

/**
 * Creates a Next.js Pages Router API route handler that upgrades WebSocket
 * connections to the bridge WebSocket handler.
 *
 * The `WebSocketServer` is created once per Node.js process and attached to
 * `res.socket.server` so it survives hot-reloads during development.
 *
 * @param options  Same options as `createBridgeWebSocketHandler`.
 * @param path     The URL path prefix to handle; defaults to "/api/bridge".
 *                 Connections on other paths are rejected.
 */
export function createNextJsBridgeRoute(
  options: BridgeWebSocketHandlerOptions,
  path = "/api/bridge"
): (req: IncomingMessage, res: ServerResponse) => void {
  return function bridgeRouteHandler(
    req: IncomingMessage,
    res: ServerResponse
  ) {
    // Only handle WebSocket upgrade requests
    if (req.headers.upgrade?.toLowerCase() !== "websocket") {
      res.writeHead(426, { "Content-Type": "text/plain" });
      res.end("This endpoint only accepts WebSocket connections.");
      return;
    }

    // Attach the WSS to the underlying HTTP server on first call
    const socket = (res as unknown as { socket: { server: ExtendedServer } })
      .socket;
    const server = socket.server;

    if (!server.bridgeWss) {
      server.bridgeWss = createBridgeWebSocketHandler(options);

      server.bridgeWss.on("error", (err: Error) => {
        (options.logger ?? console).error(
          "Bridge WebSocket server error:",
          err
        );
      });
    }

    const wss = server.bridgeWss;

    if (req.url !== path && !req.url?.startsWith(path + "?")) {
      // Not our path — do nothing (let Next.js handle it)
      return;
    }

    // Hand off the upgrade to the ws library
    const rawSocket = (res as unknown as { socket: unknown }).socket;
    wss.handleUpgrade(
      req,
      rawSocket as import("stream").Duplex,
      // eslint-disable-next-line node/no-buffer-constructor
      Buffer.from([]),
      (ws: WsWebSocket) => {
        wss.emit("connection", ws, req);
      }
    );
  };
}

/**
 * Returns a string containing a minimal custom Next.js server that attaches
 * the bridge WebSocket handler to the same HTTP server as Next.js.
 *
 * This is intended for **App Router** projects that need WebSocket support
 * without an API route.
 *
 * Save this as `server.ts` in your project root, then run it with:
 *   `tsx server.ts`   (or `ts-node server.ts`)
 *
 * @param bridgeAddr  gRPC target address, e.g. "localhost:50051"
 * @param port        HTTP port to listen on (default: 3000)
 * @param wsPath      URL path for WebSocket upgrades (default: "/bridge")
 */
export function customServerSnippet(
  bridgeAddr = "localhost:50051",
  port = 3000,
  wsPath = "/bridge"
): string {
  return `/**
 * server.ts — Custom Next.js server with bridge WebSocket support.
 * Run: tsx server.ts
 */
import { createServer } from "http";
import next from "next";
import { createBridgeWebSocketHandler } from "@ai-agent-bridge/client-node";

const dev = process.env.NODE_ENV !== "production";
const app = next({ dev });
const handle = app.getRequestHandler();

const wss = createBridgeWebSocketHandler({ bridgeAddr: "${bridgeAddr}" });

app.prepare().then(() => {
  const server = createServer((req, res) => handle(req, res));

  server.on("upgrade", (req, socket, head) => {
    if (req.url === "${wsPath}") {
      wss.handleUpgrade(req, socket, head, (ws) => wss.emit("connection", ws, req));
    } else {
      socket.destroy();
    }
  });

  server.listen(${port}, () => {
    console.log(\`> Ready on http://localhost:${port}\`);
  });
});
`;
}
