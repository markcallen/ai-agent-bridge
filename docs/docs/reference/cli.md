---
title: CLI Reference
---

The main CLI is `bridgectl`. Build it with `make build-cli`.

## Run

The quickest way to start working. Auto-starts a local server if needed, creates a session, and attaches your terminal:

```bash
bridgectl run [directory]
bridgectl run --provider codex ~/repos/my-project
bridgectl run --timeout 1h .
```

| Flag | Short | Default | Description |
| --- | --- | --- | --- |
| `--provider` | `-p` | `claude` | AI provider: `claude`, `codex`, `opencode`, `gemini`, `echo`. |
| `--timeout` | `-t` | `30m` | Session timeout. |
| `--no-tty` | | `false` | Run without a terminal (for scripting and tests). |

The directory argument defaults to `.` if omitted. Press **Ctrl-]** to detach without stopping the session.

## Server Commands

```bash
bridgectl server init
bridgectl server start
bridgectl server status
bridgectl server stop
bridgectl server issue-client --name <client-name>
bridgectl server renew-cert
```

Common server flags:

| Flag | Description |
| --- | --- |
| `--listen <addr>` | Enables secure TCP mode, for example `0.0.0.0:9445` or a Tailscale IP. |
| `--san <name>` | Adds DNS names or IP addresses to the server certificate. Repeat or pass comma-separated values. |
| `--config <path>` | Loads a YAML config. If omitted, the server checks user config locations. |
| `--db-path <path>` | Enables persisted session metadata and PTY history. |
| `--step-ca-url <url>` | Uses Step CA for server certificate issuance. |
| `--step-ca-root <path>` | Root certificate for the Step CA. |
| `--step-ca-provisioner <name>` | Step CA provisioner, such as `bridge-jwk` or `acme`. |

## Client Credential Commands

```bash
bridgectl client init \
  --step-ca-url https://ca-host:9443 \
  --provisioner bridge-jwk \
  --target bridge-host:9445

bridgectl client enroll \
  --target bridge-host:9445 \
  --ca ~/.ai-agent-bridge/certs/step-ca-root.crt \
  --cert ~/.ai-agent-bridge/certs/<name>.crt \
  --key ~/.ai-agent-bridge/certs/<name>.key

bridgectl client renew
```

`client init` obtains an mTLS certificate. When `--target` is set, it also enrolls a JWT public key with the target bridge server.

## Session Commands

```bash
bridgectl session list
bridgectl session watch <session-id>
bridgectl session attach <session-id>
bridgectl session attach --take-over <session-id>
bridgectl session attach --release <session-id>
bridgectl session stop <session-id>
```

Remote session commands accept:

| Flag | Description |
| --- | --- |
| `--remote <host[:port]>` | Remote bridge host. Port defaults to `9445`. |
| `--cert <path>` | Client mTLS certificate override. |
| `--key <path>` | Client private key override. |
| `--jwt-key <path>` | JWT signing private key override. |
| `--server-name <name>` | TLS server name override when dialing by IP or alternate DNS name. |
