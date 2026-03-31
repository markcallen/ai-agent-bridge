import "dotenv/config";
import express from "express";
import * as http from "http";
import * as path from "path";
import * as fs from "fs";
import * as url from "url";
import * as crypto from "crypto";
import * as grpc from "@grpc/grpc-js";
import pino from "pino";
import { createBridgeWebSocketHandler } from "@ai-agent-bridge/client-node";

const __dirname = path.dirname(url.fileURLToPath(import.meta.url));

const PORT = parseInt(process.env.PORT ?? "3000", 10);
const BRIDGE_ADDR = process.env.BRIDGE_ADDR ?? "bridge.local:9445";
const CA_CERT = process.env.CA_CERT ?? "";
const CLIENT_CERT = process.env.CLIENT_CERT ?? "";
const CLIENT_KEY = process.env.CLIENT_KEY ?? "";
const JWT_KEY = process.env.JWT_KEY ?? "";
const JWT_ISSUER = process.env.JWT_ISSUER ?? "dev";
const JWT_AUDIENCE = process.env.JWT_AUDIENCE ?? "bridge";
const JWT_PROJECT = process.env.JWT_PROJECT ?? "dev";
const IS_DEV = process.env.NODE_ENV === "development";

// ---------------------------------------------------------------------------
// Logger
// ---------------------------------------------------------------------------

const logger = pino(
  { level: process.env.LOG_LEVEL ?? "info" },
  IS_DEV
    ? pino.transport({ target: "pino-pretty", options: { colorize: true } })
    : undefined
);

// ---------------------------------------------------------------------------
// JWT minting (Ed25519 / EdDSA, signed with Node.js built-in crypto)
// ---------------------------------------------------------------------------

let jwtPrivateKey: crypto.KeyObject | null = null;

if (JWT_KEY) {
  try {
    const pem = fs.readFileSync(JWT_KEY, "utf8");
    jwtPrivateKey = crypto.createPrivateKey(pem);
    logger.info("JWT signing enabled");
  } catch (err) {
    logger.error({ err }, "Failed to load JWT key");
    process.exit(1);
  }
} else {
  logger.warn("JWT_KEY not set — bridge will reject unauthenticated requests");
}

function b64url(buf: Buffer): string {
  return buf.toString("base64url");
}

function mintJWT(projectId: string): string {
  if (!jwtPrivateKey) throw new Error("JWT key not loaded");
  const now = Math.floor(Date.now() / 1000);
  const ttl = 300; // 5 minutes — matches bridge jwt_max_ttl
  const header = b64url(Buffer.from(JSON.stringify({ alg: "EdDSA", typ: "JWT" })));
  const payload = b64url(
    Buffer.from(
      JSON.stringify({
        iss: JWT_ISSUER,
        sub: JWT_ISSUER,
        aud: [JWT_AUDIENCE],
        iat: now,
        exp: now + ttl,
        project_id: projectId,
      })
    )
  );
  const signingInput = `${header}.${payload}`;
  const sig = b64url(crypto.sign(null, Buffer.from(signingInput), jwtPrivateKey));
  return `${signingInput}.${sig}`;
}

// ---------------------------------------------------------------------------
// mTLS credentials
// ---------------------------------------------------------------------------

let transportCreds: grpc.ChannelCredentials;
if (CA_CERT && CLIENT_CERT && CLIENT_KEY) {
  try {
    transportCreds = grpc.credentials.createSsl(
      fs.readFileSync(CA_CERT),
      fs.readFileSync(CLIENT_KEY),
      fs.readFileSync(CLIENT_CERT)
    );
    logger.info("mTLS enabled");
  } catch (err) {
    logger.error({ err }, "Failed to load TLS certs");
    process.exit(1);
  }
} else {
  logger.warn(
    "TLS certs not configured — set CA_CERT, CLIENT_CERT, CLIENT_KEY for mTLS"
  );
  transportCreds = grpc.credentials.createInsecure();
}

// Combine mTLS transport with per-RPC JWT call credentials.
let credentials: grpc.ChannelCredentials;
if (jwtPrivateKey) {
  const callCreds = grpc.credentials.createFromMetadataGenerator((_params, callback) => {
    try {
      const token = mintJWT(JWT_PROJECT);
      const meta = new grpc.Metadata();
      meta.add("authorization", `Bearer ${token}`);
      callback(null, meta);
    } catch (err) {
      callback(err as Error);
    }
  });
  credentials = grpc.credentials.combineChannelCredentials(transportCreds, callCreds);
} else {
  credentials = transportCreds;
}

// ---------------------------------------------------------------------------
// Express + WebSocket
// ---------------------------------------------------------------------------

const BROWSER_LEVEL_MAP: Record<string, string> = {
  log: "info",
  debug: "debug",
  info: "info",
  warn: "warn",
  error: "error",
};

const app = express();
app.use(express.json());

// Browser log forwarding endpoint
app.post("/logs", (req, res) => {
  const { level, message, ...meta } = req.body ?? {};
  const pinoLevel = BROWSER_LEVEL_MAP[level] ?? "info";
  const childLogger = logger.child({ source: "browser" });
  (childLogger[pinoLevel as keyof typeof childLogger] as pino.LogFn)(meta, message ?? "(no message)");
  res.sendStatus(204);
});

const server = http.createServer(app);

const wss = createBridgeWebSocketHandler({
  bridgeAddr: BRIDGE_ADDR,
  credentials,
});

server.on("upgrade", (req, socket, head) => {
  if (req.url === "/api/bridge") {
    wss.handleUpgrade(req, socket, head, (ws: import("ws").WebSocket) => {
      wss.emit("connection", ws, req);
    });
  } else {
    socket.destroy();
  }
});

if (!IS_DEV) {
  // Production: serve compiled Vite output
  const distDir = path.resolve(__dirname, "../dist");
  app.use(express.static(distDir));
  app.get("*", (_req, res) => {
    res.sendFile(path.join(distDir, "index.html"));
  });
}

server.listen(PORT, () => {
  logger.info({ port: PORT }, "server listening");
  if (IS_DEV) {
    logger.info("dev mode — WebSocket on /api/bridge, frontend via Vite :5173");
  }
});
