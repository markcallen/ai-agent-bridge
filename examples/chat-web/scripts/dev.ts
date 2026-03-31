/**
 * Dev launcher — starts the Express server and Vite dev server in parallel.
 *
 * Run with: pnpm dev  (which calls: tsx scripts/dev.ts)
 *
 * The Express server handles WebSocket on port 3000.
 * Vite serves the frontend on port 5173 and proxies /api/bridge to Express.
 */

import { spawn, ChildProcess } from "child_process";
import * as path from "path";
import * as url from "url";

const __dirname = path.dirname(url.fileURLToPath(import.meta.url));
const root = path.resolve(__dirname, "..");

function run(
  bin: string,
  args: string[],
  env?: Record<string, string>
): ChildProcess {
  const proc = spawn(bin, args, {
    cwd: root,
    stdio: "inherit",
    shell: true,
    env: { ...process.env, ...env },
  });
  proc.on("error", (err) =>
    console.error(`[${bin}] spawn error: ${err.message}`)
  );
  return proc;
}

const server = run("tsx", ["server/index.ts"], { NODE_ENV: "development" });
const vite = run("vite", []);

function shutdown(code = 0): void {
  server.kill("SIGTERM");
  vite.kill("SIGTERM");
  process.exit(code);
}

process.on("SIGINT", () => shutdown(0));
process.on("SIGTERM", () => shutdown(0));

server.on("close", (code) => {
  if (code != null && code !== 0) {
    console.error(`[server] exited with code ${code}`);
    shutdown(code);
  }
});

vite.on("close", (code) => {
  if (code != null && code !== 0) {
    console.error(`[vite] exited with code ${code}`);
    shutdown(code);
  }
});
