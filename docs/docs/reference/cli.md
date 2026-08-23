---
title: CLI Reference
---

The main CLI is `bridgectl`. Build it with `make build`, then add `bin/` to your path.

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
bridgectl session list --project local
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

## Certificate Utility

`ai-agent-bridge-ca` remains available for explicit CA workflows:

```bash
ai-agent-bridge-ca init --name ai-agent-bridge --out certs/
ai-agent-bridge-ca issue --type server --cn bridge.local --san bridge.local,127.0.0.1 --out certs/bridge
ai-agent-bridge-ca issue --type client --cn dev-client --out certs/dev-client
ai-agent-bridge-ca jwt-keygen --out certs/jwt-signing
ai-agent-bridge-ca bundle --out certs/ca-bundle.crt certs/ca.crt
```
