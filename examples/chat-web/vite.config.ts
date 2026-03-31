import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "url";

// Resolve the monorepo package source directly so pnpm's file: copy
// (which respects .gitignore and excludes dist/) is not needed.
const pkg = (p: string) =>
  fileURLToPath(new URL(`../../packages/bridge-client-node/${p}`, import.meta.url));

// In Docker, VITE_SERVER_TARGET points to the chat-web Express container.
// Locally it defaults to localhost:3000.
const serverTarget = process.env.VITE_SERVER_TARGET ?? "http://localhost:3000";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: [
      { find: "@ai-agent-bridge/client-node/react", replacement: pkg("react/index.ts") },
      { find: "@ai-agent-bridge/client-node",       replacement: pkg("src/index.ts") },
    ],
  },
  server: {
    port: 5173,
    host: true,
    proxy: {
      "/api/bridge": {
        target: serverTarget,
        ws: true,
        rewriteWsOrigin: true,
      },
      "/logs": {
        target: serverTarget,
      },
    },
  },
  build: {
    outDir: "dist",
  },
});
