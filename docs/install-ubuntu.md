# Ubuntu Install

`ai-agent-bridge` is published as a signed apt repository for Ubuntu `24.04` (`noble`) and Ubuntu `25.04` (`plucky`) on `amd64`.

## Quick Install

```bash
curl -fsSL https://markcallen.github.io/ai-agent-bridge/install.sh | sudo bash
systemctl --user enable --now bridge
systemctl --user status bridge
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

- `/usr/bin/bridgectl`
- `/usr/bin/ai-agent-bridge-ca`
- `/etc/ai-agent-bridge/bridge.yaml`
- `/usr/lib/systemd/user/bridge.service`

The bridge runs as your login user — not a system service account. It has full access to your display, credentials, and home directory, which provider CLIs (claude, codex, opencode, gemini) require.

## Enabling The User Service

After installing the package, enable the service for your user account:

```bash
systemctl --user enable --now bridge
systemctl --user status bridge
```

To start it only for the current login session without auto-enabling on future logins:

```bash
systemctl --user start bridge
```

## Default Runtime Behavior

- The packaged config has no providers configured by default.
- The service starts and listens on the unix socket (`~/.ai-agent-bridge/server.sock`) for local connections with no authentication.
- For remote access over a VPN, start with `--listen <addr>`: edit the unit override or set `ExecStart` in a drop-in at `~/.config/systemd/user/bridge.service.d/override.conf`.
- The service can boot on a fresh host, but will not launch Claude, Codex, OpenCode, or Gemini until you install those CLIs and update `/etc/ai-agent-bridge/bridge.yaml`.

This split is intentional: the apt package ships the bridge server, not third-party provider binaries or API credentials.

## Verifying The Service

```bash
systemctl --user status bridge
journalctl --user -u bridge --no-pager -n 50
bridgectl server status
```

The release workflow validates this path in two ways:

- container smoke: installs the package from a generated apt repo on Ubuntu `noble` and `plucky`
- EC2 smoke: provisions an Ubuntu EC2 instance, installs from the hosted apt repo, starts the user service, and runs a gRPC health check

## Customizing For Real Use

For a usable agent host you still need to:

1. Install the provider CLIs you intend to run.
2. Supply the required API keys and environment for those providers.
3. Replace the default provider-less config with your real provider and auth settings.
4. For remote access, use `--listen` with a VPN-bound address and keep the server off the public internet.
