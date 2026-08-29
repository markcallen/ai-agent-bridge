---
title: Installation
---

## apt (Ubuntu)

Install `bridgectl` from the signed apt repository:

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://orchael.github.io/bridgectl/apt/bridgectl-archive-keyring.asc \
  | sudo gpg --dearmor -o /etc/apt/keyrings/bridgectl.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/bridgectl.gpg] \
  https://orchael.github.io/bridgectl/apt noble main" \
  | sudo tee /etc/apt/sources.list.d/bridgectl.list >/dev/null
sudo apt-get update
sudo apt-get install -y bridgectl
```

**Supported suites:** `noble` (24.04 LTS) and `plucky` (25.04). Replace `noble` with `plucky` if you are on Ubuntu 25.04.

## GitHub Releases

Download pre-built binaries from [GitHub Releases](https://github.com/orchael/bridgectl/releases).

## Build from Source

See [Prepare a Local Machine](./local-machine.md) for full build instructions.

```bash
git clone https://github.com/orchael/bridgectl.git
cd bridgectl
make build-cli
```

The build writes `bin/bridgectl`.
