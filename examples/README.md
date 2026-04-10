# Examples

These examples all connect to the same running `ai-agent-bridge` daemon. Start the bridge once, then try the Go terminal client, the TypeScript terminal client, and the web client against that same server.

## Start One Bridge

From the repo root:

```bash
eval "$(env-secrets export)"
make dev-run
```

This starts the bridge with local mTLS and JWT auth on `bridge.local:9445`.

If this is your first local run, make sure `bridge.local` resolves locally:

```bash
make dev-setup
```

## 1. Run `examples/chat` (Go)

From the repo root:

```bash
make chat-claude
```

Use the same running bridge with a different provider by switching the make target:

```bash
make chat-codex
make chat-gemini
```

If you want to point at a different repo path or project:

```bash
make chat-claude CHAT_REPO=$PWD CHAT_PROJECT=dev
```

## 2. Run `examples/chat-ts` (TypeScript CLI)

Install dependencies once:

```bash
cd examples/chat-ts
npm install
```

Run the TypeScript terminal client against the same bridge:

```bash
cd examples/chat-ts
npm start -- --target bridge.local:9445 --provider claude --project dev "$PWD/../.."
```

Switch only the provider to reuse the same bridge:

```bash
cd examples/chat-ts
npm start -- --target bridge.local:9445 --provider codex --project dev "$PWD/../.."
npm start -- --target bridge.local:9445 --provider gemini --project dev "$PWD/../.."
```

You can also use the repo-root shortcuts:

```bash
make chat-ts-claude
make chat-ts-codex
make chat-ts-gemini
```

## 3. Run `examples/chat-web` (React + Express)

Build the shared Node client package and install the web example dependencies:

```bash
make chat-web-install
```

The web example already includes a local `.env` configured for the dev bridge in [`examples/chat-web/.env.example`](/home/marka/src/ai-agent-bridge/examples/chat-web/.env.example). If you need to recreate it, use:

```dotenv
BRIDGE_ADDR=bridge.local:9445
CA_CERT=../../certs/ca-bundle.crt
CLIENT_CERT=../../certs/dev-client.crt
CLIENT_KEY=../../certs/dev-client.key
JWT_KEY=../../certs/jwt-signing.key
JWT_ISSUER=dev
JWT_AUDIENCE=bridge
JWT_PROJECT=dev
PORT=3000
VITE_PORT=5173
```

Start the web app:

```bash
make chat-web-dev
```

Then open `http://localhost:5173`.

To connect to the same running bridge with different providers:

1. Enter the repo path you want the bridge session to run in.
2. Choose `claude`, `codex`, or `gemini` from the provider dropdown.
3. Click `Start`.

Each selection starts a new bridge session on the same daemon at `bridge.local:9445`.

## Provider Matrix

All three examples talk to the same bridge API. The provider changes per session:

| Example | Claude | Codex | Gemini |
| --- | --- | --- | --- |
| `examples/chat` | `make chat-claude` | `make chat-codex` | `make chat-gemini` |
| `examples/chat-ts` | `make chat-ts-claude` | `make chat-ts-codex` | `make chat-ts-gemini` |
| `examples/chat-web` | Select `claude` in the UI | Select `codex` in the UI | Select `gemini` in the UI |

## Notes

- `claude` requires `ANTHROPIC_API_KEY`.
- `codex` requires `OPENAI_API_KEY`.
- `gemini` requires `GEMINI_API_KEY`.
- The bridge reports provider availability through health and provider-list APIs, so the web UI can show whether a provider is ready before you start a session.
