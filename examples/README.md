# Examples

These examples show how to connect to an `ai-agent-bridge` server, start AI agent sessions, and interact with them. All examples auto-discover credentials from `~/.ai-agent-bridge/` — no flags to memorise.

## Prerequisites

Start the bridge server first:

```bash
# Local mode (auto TLS, no Step CA required)
bridgectl server start

# Or with Step CA for remote access — see docs/step-ca.md
```

For **remote** examples (`chat-ca`, `sessions --remote`, `web --remote`), you need a client certificate enrolled with the remote server:

```bash
bridgectl client init --step-ca-url https://your-ca.example.com
# Follow the prompts; credentials are saved to ~/.ai-agent-bridge/certs/
```

---

## `examples/chat` — Interactive session (local)

Start a new Claude Code session on the local bridge and attach your terminal:

```bash
go run ./examples/chat <repo-path>
# or
make chat-claude CHAT_REPO=/path/to/repo
make chat-opencode CHAT_REPO=/path/to/repo
make chat-codex CHAT_REPO=/path/to/repo
make chat-gemini CHAT_REPO=/path/to/repo
```

**Flags:**
- `--state-dir` — override state directory (default: `~/.ai-agent-bridge`)
- `--project` — project ID (default: `local`)
- `--provider` — provider name (default: `claude`)
- `--timeout` — session timeout (default: `30m`)

---

## `examples/chat-ca` — Interactive session (remote via Step CA)

Same as `chat` but connects to a remote bridge server. Credentials are auto-discovered from `~/.ai-agent-bridge/certs/`.

```bash
go run ./examples/chat-ca --remote macbook.ts.net <repo-path>
# or
make chat-ca-claude CHAT_REPO=/path/to/repo CHAT_CA_REMOTE=macbook.ts.net
make chat-ca-opencode CHAT_REPO=/path/to/repo CHAT_CA_REMOTE=macbook.ts.net
```

**Flags:**
- `--remote` — remote hostname or `host:port` (required; defaults to port 9445)
- `--state-dir` — override state directory
- `--cert`, `--key`, `--jwt-key` — manual credential overrides (optional)
- `--project`, `--provider`, `--timeout`

---

## `examples/sessions` — List, watch, and attach to sessions

Three subcommands for managing existing sessions on local or remote servers:

```bash
# List all sessions
go run ./examples/sessions list
go run ./examples/sessions list --remote macbook.ts.net

# Watch a session (read-only output stream)
go run ./examples/sessions watch <session-id>
go run ./examples/sessions watch --remote macbook.ts.net <session-id>

# Attach to a session (claim the writer slot)
go run ./examples/sessions attach <session-id>
go run ./examples/sessions attach --remote macbook.ts.net <session-id>
go run ./examples/sessions attach --take-over <session-id>  # force writer claim
```

**Makefile shortcuts:**

```bash
make sessions-list
make sessions-list SESSIONS_REMOTE=macbook.ts.net

make sessions-watch SESSIONS_WATCH_ID=<id>
make sessions-watch SESSIONS_WATCH_ID=<id> SESSIONS_REMOTE=macbook.ts.net

make sessions-attach SESSIONS_ATTACH_ID=<id>
make sessions-attach SESSIONS_ATTACH_ID=<id> SESSIONS_REMOTE=macbook.ts.net
```

**Notes:**
- `watch` streams output to stdout as a read-only observer.
- `attach` checks for an active writer first; if one exists, it suggests `watch` or `--take-over`.
- Press `Ctrl+\` to detach from `attach` without stopping the session; `Ctrl+C` sends interrupt to the session.

---

## `examples/web` — Web UI (Go server + Vite/React frontend)

A browser-based interface for listing, starting, watching, and interacting with sessions. The Go server proxies all bridge API calls; the React frontend renders session output in an xterm.js terminal.

### Development mode (hot reload)

```bash
# Terminal 1 — Go API server
cd examples/web && go run . --port 8080

# Terminal 2 — Vite dev server (proxies /api → :8080)
cd examples/web/ui && pnpm install && pnpm dev
```

Then open `http://localhost:5173`.

### Production mode

```bash
make web-build        # builds the Vite app into examples/web/ui/dist/
make web-start        # serves the built UI + API from :8080
```

Then open `http://localhost:8080`.

### Flags

```
--port       HTTP server port (default: 8080)
--vite-port  Vite dev server port to proxy to; 0 = serve built ui/dist/ (default: 5173)
```

### Connecting to a remote server

The web UI has a **Remote host** field in the header. Enter a hostname (e.g. `macbook.ts.net`) and all API calls will be routed to that remote bridge server using credentials from `~/.ai-agent-bridge/certs/`. Leave it empty to use the local server.

---

## Provider Notes

| Provider | Required secret |
|---|---|
| `claude` | `CLAUDE_CODE_OAUTH_TOKEN` |
| `opencode` | depends on opencode config |
| `codex` | `OPENAI_API_KEY` |
| `gemini` | `GEMINI_API_KEY` |

Set secrets in your `.env` file or via `env-secrets`:

```bash
cp .env.example .env
$EDITOR .env
```
