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

## Clone and Build

```bash
git clone https://github.com/markcallen/ai-agent-bridge.git
cd ai-agent-bridge

nvm install
nvm use
corepack enable
corepack prepare pnpm@11.22.0 --activate

make build
```

The build writes:

- `bin/bridgectl`
- `bin/ai-agent-bridge-ca`

Add the binaries to your shell path for the examples:

```bash
export PATH="$PWD/bin:$PATH"
```

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

For local testing, a throwaway repo is enough:

```bash
mkdir -p ~/repos/bridge-demo
cd ~/repos/bridge-demo
git init
```
