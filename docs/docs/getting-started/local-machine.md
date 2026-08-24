---
title: Prepare a Local Machine
---

Use this setup on every machine that will run a bridge server or use the example clients.

## Prerequisites

- Go 1.25 or newer
- Node.js 24.x and Corepack
- pnpm 11.22.0 through Corepack
- Docker Compose v2 if you use the compose examples
- At least one provider CLI credential:
  - `CLAUDE_CODE_OAUTH_TOKEN` for Claude
  - `OPENAI_API_KEY` or Codex auth files for Codex
  - `OPENAI_API_KEY` for OpenCode
  - `GEMINI_API_KEY` for Gemini

## Install on Linux (apt)

On Ubuntu, you can install `bridgectl` from the apt repository instead of building from source:

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://markcallen.github.io/ai-agent-bridge/apt/ai-agent-bridge-archive-keyring.asc \
  | sudo gpg --dearmor -o /etc/apt/keyrings/ai-agent-bridge.gpg
echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/ai-agent-bridge.gpg] \
  https://markcallen.github.io/ai-agent-bridge/apt noble main" \
  | sudo tee /etc/apt/sources.list.d/ai-agent-bridge.list >/dev/null
sudo apt-get update
sudo apt-get install -y ai-agent-bridge
```

**Supported suites:** `noble` (24.04 LTS) and `plucky` (25.04). Replace `noble` with `plucky` if you are on Ubuntu 25.04.

If you install via apt, skip the **Clone and Build** section below and go straight to **Install Provider CLIs**.

## Clone and Build

```bash
git clone https://github.com/markcallen/ai-agent-bridge.git
cd ai-agent-bridge

nvm install
nvm use
corepack enable
corepack prepare pnpm@11.22.0 --activate

make build-cli
```

The build writes `bin/bridgectl`. Use `make build` instead if you have `protoc` installed and want to regenerate the gRPC stubs.

## Install Provider CLIs

The repository pins the provider CLIs in the root `package.json`.

```bash
pnpm install --frozen-lockfile
```

Export the provider credentials that match the provider you plan to run:

```bash
export OPENAI_API_KEY="..."
export CLAUDE_CODE_OAUTH_TOKEN="..."
export GEMINI_API_KEY="..."
```

Only the variables required by the selected provider need to be set.

## Repository Paths

The default local config allows repositories under `/home` and `/tmp`. If your working repositories live somewhere else, add that parent directory under `allowed_paths` in the bridge config.

For local testing, clone the demo repository:

```bash
mkdir -p ~/repos
git clone https://github.com/orchael/bridge-demo.git ~/repos/bridge-demo
```
