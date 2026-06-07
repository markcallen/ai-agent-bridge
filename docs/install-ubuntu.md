# Ubuntu Install

`ai-agent-bridge` is published as a signed apt repository for Ubuntu `24.04` (`noble`) and Ubuntu `25.04` (`plucky`) on `amd64`.

## Quick Install

```bash
curl -fsSL https://markcallen.github.io/ai-agent-bridge/install.sh | sudo bash
sudo systemctl enable --now ai-agent-bridge
sudo systemctl status ai-agent-bridge
```

The helper script detects the Ubuntu codename, installs the repository key into `/etc/apt/keyrings`, adds the apt source, updates package metadata, and installs `ai-agent-bridge`.

## Manual Install

### Ubuntu 24.04 (`noble`)

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://markcallen.github.io/ai-agent-bridge/apt/ai-agent-bridge-archive-keyring.asc \
  | sudo gpg --dearmor -o /etc/apt/keyrings/ai-agent-bridge.gpg
echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/ai-agent-bridge.gpg] https://markcallen.github.io/ai-agent-bridge/apt noble main" \
  | sudo tee /etc/apt/sources.list.d/ai-agent-bridge.list >/dev/null
sudo apt-get update
sudo apt-get install -y ai-agent-bridge
```

### Ubuntu 25.04 (`plucky`)

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://markcallen.github.io/ai-agent-bridge/apt/ai-agent-bridge-archive-keyring.asc \
  | sudo gpg --dearmor -o /etc/apt/keyrings/ai-agent-bridge.gpg
echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/ai-agent-bridge.gpg] https://markcallen.github.io/ai-agent-bridge/apt plucky main" \
  | sudo tee /etc/apt/sources.list.d/ai-agent-bridge.list >/dev/null
sudo apt-get update
sudo apt-get install -y ai-agent-bridge
```

## What The Package Installs

- `/usr/bin/ai-agent-bridge`
- `/usr/bin/ai-agent-bridge-ca`
- `/etc/ai-agent-bridge/bridge.yaml`
- `/lib/systemd/system/ai-agent-bridge.service`

The systemd unit runs as the `ai-agent-bridge` system user and stores state under `/var/lib/ai-agent-bridge`.

## Default Runtime Behavior

- The packaged config listens on `127.0.0.1:9445`.
- TLS and JWT auth are disabled in the packaged default config.
- No providers are configured by default.
- The service can boot on a fresh host, but it will not launch Claude, Codex, OpenCode, or Gemini until you install those CLIs and update `/etc/ai-agent-bridge/bridge.yaml`.

This split is intentional: the apt package ships the bridge daemon, not third-party provider binaries or API credentials.

## Verifying The Service

```bash
sudo systemctl enable --now ai-agent-bridge
sudo systemctl status ai-agent-bridge
sudo journalctl -u ai-agent-bridge --no-pager -n 50
```

The release workflow validates this path in two ways:

- container smoke: installs the package from a generated apt repo on Ubuntu `noble` and `plucky`
- EC2 smoke: provisions an Ubuntu EC2 instance, installs from the hosted apt repo, starts the systemd service, and runs a gRPC health check through an SSH tunnel

## Customizing For Real Use

For a usable agent host you still need to:

1. Install the provider CLIs you intend to run.
2. Supply the required API keys and environment for those providers.
3. Replace the default provider-less config with your real provider and auth settings.
4. Review the service account and repository path permissions before exposing the daemon beyond localhost.

## ai-desktops Agent-Host Profile

If you are provisioning a dedicated Ubuntu 24.04 machine where the bridge runs as a system service and spawns AI agents against repositories under `/workspace`, follow the **ai-desktops** profile instead of the manual steps above.

The ai-desktops profile adds:

- A provider runtime installer (`/usr/lib/ai-agent-bridge/install-provider-runtime`) that installs pinned Claude, Codex, OpenCode, and Gemini CLIs into `/opt/ai-agent-bridge`.
- An example config (`/usr/share/doc/ai-agent-bridge/examples/bridge-example.yaml`) with all four provider stanzas pre-written and the `/workspace` path policy enabled.
- A systemd drop-in (`/usr/share/doc/ai-agent-bridge/examples/ai-desktops.conf`) that injects credentials from `/etc/ai-agent-bridge/agents.env` and grants agent write access to `/workspace`.

See [ai-desktops.md](ai-desktops.md) for the full provisioning guide.
