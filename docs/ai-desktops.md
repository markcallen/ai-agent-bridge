# ai-desktops Agent-Host Guide

This guide covers provisioning, operating, and troubleshooting `ai-agent-bridge` on an **ai-desktops** Ubuntu 24.04 host — a machine where the bridge runs as a system service and spawns AI agent CLIs against repositories under `/workspace`.

---

## Architecture and Security Boundary

```
ai-desktops host (Ubuntu 24.04)
┌─────────────────────────────────────────────────────┐
│                                                     │
│  /workspace/<repo>       ← agent working directories │
│                                                     │
│  ai-agent-bridge daemon  127.0.0.1:9445             │
│    ↕ PTY                                            │
│  claude / codex / opencode / gemini                 │
│    (from /opt/ai-agent-bridge/node_modules/)        │
│                                                     │
│  /etc/ai-agent-bridge/                              │
│    bridge.yaml           ← operator-supplied config │
│    agents.env            ← root:root 0600, API keys │
│                                                     │
│  /opt/ai-agent-bridge/   ← provider CLIs, root:root │
│  /var/lib/ai-agent-bridge/sessions.db  ← persistence │
│                                                     │
└─────────────────────────────────────────────────────┘
         ↑ localhost only — no external exposure
```

**Security constraints:**

- The bridge listens on `127.0.0.1:9445` only. Do not expose it publicly without adding mTLS and JWT (see [service.md](service.md)).
- Provider API keys live in `/etc/ai-agent-bridge/agents.env` (`root:root 0600`). They are injected into the daemon environment at service startup and never written to disk by the bridge.
- The service account (`ai-agent-bridge`) cannot write to `/opt/ai-agent-bridge`. Only root can update provider CLIs.
- Agent subprocesses inherit the systemd sandbox and can write to `/workspace`, `/var/lib/ai-agent-bridge`, `/tmp`, and `/var/tmp` only.

---

## Package Install

Install `ai-agent-bridge` from the apt repository:

```bash
curl -fsSL https://markcallen.github.io/ai-agent-bridge/install.sh | sudo bash
sudo systemctl enable --now ai-agent-bridge
sudo systemctl status ai-agent-bridge
```

The base package installs a provider-neutral daemon. It starts and passes a health check, but no AI providers are configured yet.

**What the package installs:**

```
/usr/bin/ai-agent-bridge
/usr/bin/ai-agent-bridge-ca
/etc/ai-agent-bridge/bridge.yaml           ← default (no providers)
/lib/systemd/system/ai-agent-bridge.service
/usr/lib/ai-agent-bridge/install-provider-runtime
/usr/share/ai-agent-bridge/provider-runtime/.nvmrc
/usr/share/ai-agent-bridge/provider-runtime/package.json
/usr/share/ai-agent-bridge/provider-runtime/package-lock.json
/usr/share/doc/ai-agent-bridge/examples/bridge-ai-desktops.yaml
/usr/share/doc/ai-agent-bridge/examples/ai-desktops.conf
```

---

## Provisioning Flow

Run these steps once after installing the package, or re-run them on upgrade.

### 1. Install the Provider Runtime

```bash
sudo /usr/lib/ai-agent-bridge/install-provider-runtime
```

This script:

1. Installs or verifies Node.js 24 via NodeSource (if not already present).
2. Copies the packaged runtime manifest to `/opt/ai-agent-bridge`.
3. Runs `npm ci --omit=dev` in a staging directory and swaps `node_modules` into place only on success — a failed install leaves the existing runtime working.
4. Verifies each installed CLI binary is present and prints its version.
5. Sets ownership of `/opt/ai-agent-bridge` to `root:root`.

To verify an existing installation without making changes:

```bash
sudo /usr/lib/ai-agent-bridge/install-provider-runtime --verify
```

### 2. Apply the ai-desktops Config

Copy and customize the example config:

```bash
sudo cp /usr/share/doc/ai-agent-bridge/examples/bridge-ai-desktops.yaml \
  /etc/ai-agent-bridge/bridge.yaml
sudo $EDITOR /etc/ai-agent-bridge/bridge.yaml
```

Uncomment one or more provider blocks in the config. See [Provider Configuration](#provider-configuration) below.

### 3. Install the systemd Drop-In

```bash
sudo mkdir -p /etc/systemd/system/ai-agent-bridge.service.d
sudo cp /usr/share/doc/ai-agent-bridge/examples/ai-desktops.conf \
  /etc/systemd/system/ai-agent-bridge.service.d/ai-desktops.conf
```

The drop-in extends the base unit with:
- `EnvironmentFile=-/etc/ai-agent-bridge/agents.env` — injects credentials from the env file
- `ReadWritePaths=/var/lib/ai-agent-bridge /workspace /tmp /var/tmp` — grants agent write access to `/workspace`

### 4. Create the Credentials File

Create the credentials file before starting the service:

```bash
sudo install -m 0600 -o root -g root /dev/null /etc/ai-agent-bridge/agents.env
```

Add required API key variables for the providers you enabled:

```bash
# For Claude Code:
echo "ANTHROPIC_API_KEY=sk-ant-..." | sudo tee -a /etc/ai-agent-bridge/agents.env >/dev/null

# For Codex:
echo "OPENAI_API_KEY=sk-..." | sudo tee -a /etc/ai-agent-bridge/agents.env >/dev/null

# For Gemini CLI:
echo "GOOGLE_API_KEY=AIza..." | sudo tee -a /etc/ai-agent-bridge/agents.env >/dev/null
```

The leading `-` in `EnvironmentFile=-/path` means the service starts even if the file is absent. The bridge itself fails at startup if a configured provider's `required_env` variable is missing from the process environment.

### 5. Reload and Restart

```bash
sudo systemctl daemon-reload
sudo systemctl restart ai-agent-bridge
sudo systemctl status ai-agent-bridge
```

### 6. Verify Health

```bash
sudo journalctl -u ai-agent-bridge --no-pager -n 30
```

Look for `bridge daemon starting` and `registered provider` log lines.

---

## Provider Runtime Layout

After running `install-provider-runtime`, the runtime is at:

```
/opt/ai-agent-bridge/
  .nvmrc                         ← required Node.js major version
  package.json                   ← pinned provider CLI versions
  package-lock.json              ← reproducible install manifest
  node_modules/
    @anthropic-ai/claude-code/   ← Claude Code CLI
    @openai/codex/               ← Codex CLI
    opencode-ai/                 ← OpenCode (native binary)
    @google/gemini-cli/          ← Gemini CLI
    .bin/
      claude
      codex
      opencode
      gemini
```

---

## Provider Configuration

Edit `/etc/ai-agent-bridge/bridge.yaml`. Uncomment the relevant block and supply credentials via `/etc/ai-agent-bridge/agents.env`.

### Claude Code

```yaml
providers:
  claude:
    binary: "/usr/bin/node"
    args: ["/opt/ai-agent-bridge/node_modules/@anthropic-ai/claude-code/cli.js"]
    startup_timeout: "60s"
    startup_probe: "output"
    required_env: ["ANTHROPIC_API_KEY"]
    prompt_pattern: '(?m)(❯|>\s*$)'
```

Requires `ANTHROPIC_API_KEY` in `/etc/ai-agent-bridge/agents.env`.

### Codex

```yaml
providers:
  codex:
    binary: "/usr/bin/node"
    args: ["/opt/ai-agent-bridge/node_modules/@openai/codex/bin/codex.js"]
    startup_timeout: "60s"
    startup_probe: "output"
    required_env: ["OPENAI_API_KEY"]
    prompt_pattern: '(?m)(>\s*$|›)'
```

Requires `OPENAI_API_KEY` in `/etc/ai-agent-bridge/agents.env`.

### OpenCode

OpenCode ships as a native binary — no Node invocation needed.

```yaml
providers:
  opencode:
    binary: "/opt/ai-agent-bridge/node_modules/.bin/opencode"
    args: []
    startup_timeout: "45s"
    startup_probe: "output"
```

Requires `OPENAI_API_KEY` or `ANTHROPIC_API_KEY` (depending on which model is configured in OpenCode).

### Gemini CLI

```yaml
providers:
  gemini:
    binary: "/usr/bin/node"
    args: ["/opt/ai-agent-bridge/node_modules/@google/gemini-cli/dist/index.js"]
    startup_timeout: "60s"
    startup_probe: "output"
    required_env: ["GOOGLE_API_KEY"]
```

Requires `GOOGLE_API_KEY` in `/etc/ai-agent-bridge/agents.env`.

---

## Environment File Contract

The credentials file is the only supported mechanism for injecting secrets into the bridge service:

| Path | Owner | Mode | Purpose |
|---|---|---|---|
| `/etc/ai-agent-bridge/agents.env` | `root:root` | `0600` | Provider API keys and secrets |

Each line is a `KEY=value` pair. The bridge daemon inherits these variables at startup and passes them to provider subprocesses. The bridge never logs, redacts-and-logs, or writes secret values.

**Do not:**
- Write secrets into `bridge.yaml` or any other world-readable file.
- Store secrets in the repository, apt package, AMI, or instance metadata.
- Set permissions above `0600` on `agents.env`.

---

## Upgrade Workflow

When a new `ai-agent-bridge` package is published:

```bash
sudo apt-get update
sudo apt-get upgrade -y ai-agent-bridge
sudo /usr/lib/ai-agent-bridge/install-provider-runtime
sudo systemctl daemon-reload
sudo systemctl restart ai-agent-bridge
sudo systemctl status ai-agent-bridge
```

The `install-provider-runtime` step re-copies the updated manifest and reinstalls pinned provider CLIs into a staging directory. Existing workspaces and session persistence are unaffected.

---

## Troubleshooting

### Service fails to start

```bash
sudo journalctl -u ai-agent-bridge --no-pager -n 50
```

Common causes:

| Log message | Fix |
|---|---|
| `node runtime validation failed` | Node.js not installed or wrong version. Run `install-provider-runtime`. |
| `provider environment validation failed` | A required env var is missing. Check `agents.env`. |
| `open session store` | `/var/lib/ai-agent-bridge` not writable. Check systemd `ReadWritePaths`. |
| `listen ... bind: address already in use` | Port 9445 in use. Check `ss -tlnp` and resolve the conflict. |

### Check Node.js version

```bash
node --version           # should print v24.x.x
/usr/lib/ai-agent-bridge/install-provider-runtime --verify
```

### Check provider CLI binaries

```bash
/opt/ai-agent-bridge/node_modules/.bin/claude --version
/opt/ai-agent-bridge/node_modules/.bin/codex --version
/opt/ai-agent-bridge/node_modules/.bin/opencode --version
/opt/ai-agent-bridge/node_modules/.bin/gemini --version
```

### Check runtime.provider_root is correct

If you see `node runtime validation failed: read .nvmrc: ...`, the bridge cannot find the `.nvmrc` at the configured `runtime.provider_root`. Verify:

```bash
cat /etc/ai-agent-bridge/bridge.yaml | grep provider_root
ls /opt/ai-agent-bridge/.nvmrc
```

### Check /workspace access

```bash
# Verify the drop-in is installed:
cat /etc/systemd/system/ai-agent-bridge.service.d/ai-desktops.conf

# Verify the bridge policy allows /workspace:
grep workspace /etc/ai-agent-bridge/bridge.yaml

# Verify the directory exists:
ls -la /workspace
```

### Check bridge health

```bash
# Using the bridge-ca healthcheck binary (if built):
/usr/local/bin/plain-healthcheck -target 127.0.0.1:9445

# Using grpc-health-probe if installed:
grpc-health-probe -addr 127.0.0.1:9445
```

### View live logs

```bash
sudo journalctl -u ai-agent-bridge -f
```

---

## Localhost-Only Default and Remote Exposure Warning

By default the bridge binds to `127.0.0.1:9445`. All connections originate from the same host. There is no encryption or authentication required for localhost-only use.

**If you expose the bridge over the network** (by changing `server.listen` to `0.0.0.0:9445` or forwarding port 9445), you must also configure:

- `tls.ca_bundle`, `tls.cert`, `tls.key` — mTLS to authenticate clients
- `auth.jwt_public_keys` — JWT (Ed25519) to authorize RPCs

See [service.md — Security](service.md) for the full mTLS + JWT configuration reference.
