---
title: Configuration Reference
---

`bridgectl server start` can load YAML from:

1. `--config <path>`
2. `~/.ai-agent-bridge/bridge.yaml`
3. `$XDG_CONFIG_HOME/bridgectl/config.yaml`
4. The platform user config directory

Flags override values from the config file.

## Minimal Local Config

```yaml
server:
  listen: "127.0.0.1:9445"

allowed_paths:
  - "/home"
  - "/tmp"

providers:
  codex:
    binary: "node"
    args: ["./node_modules/@openai/codex/bin/codex.js"]
    startup_timeout: "60s"
```

## Remote Step CA Config

```yaml
server:
  listen: "100.x.y.z:9445"
  san:
    - "machine.tailnet-name.ts.net"

step_ca:
  url: "https://ca-host.tailnet-name.ts.net:9443"
  root: "/home/me/.ai-agent-bridge/certs/step-ca-root.crt"
  provisioner: "bridge-jwk"
  provisioner_password_file: "/home/me/.ai-agent-bridge/step-ca-password"

allowed_paths:
  - "/home/me/repos"
```

## Important Fields

| Field | Purpose |
| --- | --- |
| `server.listen` | TCP bind address. If omitted and no `--listen` is set, local Unix socket mode is used. |
| `server.san` | Server certificate SANs used by remote clients for TLS verification. |
| `tls.ca_bundle`, `tls.cert`, `tls.key` | Explicit mTLS material for non-Step-CA deployments. |
| `auth.jwt_public_keys` | Static JWT issuer public keys. |
| `step_ca.clients` | Startup-loaded JWT public keys for known Step CA clients. |
| `sessions.max_per_project` | Per-project session limit. |
| `sessions.max_global` | Global session limit. |
| `sessions.event_buffer_size` | Per-session replay buffer size. |
| `input.max_size_bytes` | Maximum input payload accepted per write. |
| `providers.<name>.binary` | Provider executable. |
| `providers.<name>.args` | Arguments prepended when starting the provider. |
| `providers.<name>.required_env` | Environment variables required before the provider is considered healthy. |
| `allowed_paths` | Parent paths under which sessions may run. |

## Provider Fallbacks

When `feature_flags.provider_fallbacks` is true, a provider can declare fallback providers:

```yaml
providers:
  claude:
    binary: "./node_modules/@anthropic-ai/claude-code/bin/claude.exe"
    required_env: ["CLAUDE_CODE_OAUTH_TOKEN"]
    fallbacks: ["codex"]
```

If `claude` is unavailable, the server can select `codex` for the session.

## Persistence

Use `--db-path` to persist session metadata and PTY chunks:

```bash
bridgectl server start --db-path ~/.ai-agent-bridge/sessions.db
```

On restart, terminal session history can be replayed as terminal history.
