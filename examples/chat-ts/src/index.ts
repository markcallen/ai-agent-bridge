/**
 * chat-ts — TypeScript interactive chat example for ai-agent-bridge.
 *
 * Mirrors the behaviour of examples/chat/main.go:
 *   - Starts a session with the requested provider
 *   - Attaches and streams raw PTY output to stdout
 *   - Reads raw stdin bytes and forwards them via WriteInput
 *   - Handles terminal resize (SIGWINCH equivalent via process.stdout 'resize')
 *   - Graceful shutdown on SIGTERM / SIGINT
 *
 * Usage:
 *   npx tsx src/index.ts [options] <repo-path>
 *
 * Options:
 *   --target   <addr>      gRPC address (default: 127.0.0.1:9445)
 *   --project  <id>        Project ID (default: dev)
 *   --provider <name>      Provider name (default: claude)
 *   --cacert   <path>      CA bundle for mTLS
 *   --cert     <path>      Client certificate for mTLS
 *   --key      <path>      Client private key for mTLS
 */

import * as path from "path";
import { BridgeGrpcClient } from "@ai-agent-bridge/client-node";
import { randomUUID } from "crypto";
import { buildCredentials, currentTTYSize, normalizeTTYInput, parseArgs } from "./lib";

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

async function main(): Promise<void> {
  let opts;
  try {
    opts = parseArgs(process.argv);
  } catch (error) {
    console.error((error as Error).message);
    console.error("  --target   <addr>   gRPC address (default: 127.0.0.1:9445)");
    console.error("  --project  <id>     Project ID (default: dev)");
    console.error("  --provider <name>   Provider (default: claude)");
    console.error("  --cacert   <path>   CA bundle for mTLS");
    console.error("  --cert     <path>   Client certificate for mTLS");
    console.error("  --key      <path>   Client private key for mTLS");
    process.exit(1);
  }
  const creds = buildCredentials(opts.cacert, opts.cert, opts.key);

  const client = new BridgeGrpcClient({
    bridgeAddr: opts.target,
    credentials: creds,
  });

  const sessionId = randomUUID();
  const clientId = randomUUID();
  const { cols, rows } = currentTTYSize();

  // Start session
  const startResult = await client.startSession({
    projectId: opts.project,
    sessionId,
    repoPath: path.resolve(opts.repoPath),
    provider: opts.provider,
    initialCols: cols,
    initialRows: rows,
  });
  process.stderr.write(
    `[bridge] session ${startResult.sessionId} started (${startResult.status})\r\n`
  );

  // Put stdin in raw mode so every keypress is forwarded immediately.
  if (process.stdin.isTTY) {
    process.stdin.setRawMode(true);
  }
  process.stdin.resume();

  const ac = new AbortController();
  let stopping = false;

  async function shutdown(): Promise<void> {
    if (stopping) return;
    stopping = true;
    ac.abort();
    if (process.stdin.isTTY) {
      process.stdin.setRawMode(false);
    }
    try {
      await client.stopSession({ sessionId, force: true });
    } catch {
      // best-effort
    }
    client.close();
    process.exit(0);
  }

  process.on("SIGTERM", () => void shutdown());
  process.on("SIGINT", () => void shutdown());

  // Forward stdin bytes to the session
  process.stdin.on("data", (chunk: Buffer) => {
    // Ctrl+C in raw mode → graceful shutdown
    if (chunk.length === 1 && chunk[0] === 0x03) {
      void shutdown();
      return;
    }
    const data = normalizeTTYInput(chunk);
    client.writeInput({ sessionId, clientId, data }).catch(() => {
      // ignore write errors during shutdown
    });
  });

  // Forward terminal resize events
  process.stdout.on("resize", () => {
    const { cols: c, rows: r } = currentTTYSize();
    client.resizeSession({ sessionId, clientId, cols: c, rows: r }).catch(() => {
      // ignore resize errors during shutdown
    });
  });

  // Attach and stream output
  try {
    for await (const event of client.attachSession({
      sessionId,
      clientId,
      afterSeq: 0,
      signal: ac.signal,
    })) {
      switch (event.type) {
        case "output":
          process.stdout.write(event.payload);
          break;

        case "replay_gap":
          process.stderr.write(
            `\r\n[bridge] replay gap: oldest=${event.oldestSeq} last=${event.lastSeq}\r\n`
          );
          break;

        case "session_exit": {
          const exitCode = event.exitCode;
          process.stderr.write(
            `\r\n[bridge] session exited (code ${exitCode})\r\n`
          );
          if (process.stdin.isTTY) process.stdin.setRawMode(false);
          client.close();
          process.exit(exitCode);
          return;
        }

        case "error":
          process.stderr.write(`\r\n[bridge] error: ${event.error}\r\n`);
          break;
      }
    }
  } catch (err) {
    if (!stopping) {
      process.stderr.write(
        `\r\n[bridge] stream failed: ${err instanceof Error ? err.message : String(err)}\r\n`
      );
      if (process.stdin.isTTY) process.stdin.setRawMode(false);
      client.close();
      process.exit(1);
    }
  }

  if (process.stdin.isTTY) process.stdin.setRawMode(false);
  client.close();
}

main().catch((err) => {
  process.stderr.write(`fatal: ${err instanceof Error ? err.message : String(err)}\n`);
  process.exit(1);
});
