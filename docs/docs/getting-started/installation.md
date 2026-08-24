---
title: Installation
---

## apt (Ubuntu / Debian)

Install `bridgectl` from the signed apt repository:

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

## GitHub Releases

Download pre-built binaries from [GitHub Releases](https://github.com/markcallen/ai-agent-bridge/releases).

## Build from Source

See [Prepare a Local Machine](./local-machine.md) for full build instructions.

```bash
git clone https://github.com/markcallen/ai-agent-bridge.git
cd ai-agent-bridge
make build-cli
```

The build writes `bin/bridgectl`.
