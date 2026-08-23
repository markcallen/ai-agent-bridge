---
title: Step CA over Tailscale
---

This guide starts a Step CA in Docker Compose, starts one bridge server on the first machine, starts another bridge server on a second machine, enrolls the first machine as a client of the second, and then uses the web UI to connect to both.

## Machines

The commands below use these placeholder names:

| Placeholder | Meaning |
| --- | --- |
| `ca-host` | The machine running Step CA. This can be the same as machine A. |
| `machine-a` | Your first bridge host and web UI host. |
| `machine-b` | The remote bridge host. |

Use each machine's Tailscale DNS name, for example `machine-a.tailnet-name.ts.net`.

## 1. Confirm Tailscale

Run this on every machine:

```bash
tailscale status
tailscale ip -4
```

The bridge and Step CA should bind only to Tailscale or another private interface. Do not expose port `9445` or `9443` to the public internet.

## 2. Start Step CA in Docker Compose

On `ca-host`:

```bash
cd /path/to/ai-agent-bridge

export STEP_CA_PASSWORD='change-this-dev-password'
export STEP_CA_SANS='ca-host.tailnet-name.ts.net'
export STEP_CA_BIND='100.x.y.z'

make up-step-ca
```

`STEP_CA_BIND` should be the Tailscale IP from `tailscale ip -4`. The Step CA listens on `https://ca-host.tailnet-name.ts.net:9443`.

Check health:

```bash
make step-ca-health
```

## 3. Prepare Machine A and Machine B

On both bridge hosts:

```bash
git clone https://github.com/markcallen/ai-agent-bridge.git
cd ai-agent-bridge
nvm install
nvm use
corepack enable
corepack prepare pnpm@11.22.0 --activate
pnpm install --frozen-lockfile
make build
```

Export any provider credentials needed on each machine.

## 4. Start the Server on Machine A

On `machine-a`:

```bash
mkdir -p ~/.ai-agent-bridge/certs
curl -sk https://ca-host.tailnet-name.ts.net:9443/roots \
  | jq -r '.crts[0]' > ~/.ai-agent-bridge/certs/step-ca-root.crt

bin/bridgectl server start \
  --listen 100.a.a.a:9445 \
  --san machine-a.tailnet-name.ts.net \
  --step-ca-url https://ca-host.tailnet-name.ts.net:9443 \
  --step-ca-root ~/.ai-agent-bridge/certs/step-ca-root.crt \
  --step-ca-provisioner bridge-jwk
```

If the JWK provisioner requires a password non-interactively, write it to a `0600` file and pass `--step-ca-provisioner-password-file`.

## 5. Start the Server on Machine B

On `machine-b`:

```bash
mkdir -p ~/.ai-agent-bridge/certs
curl -sk https://ca-host.tailnet-name.ts.net:9443/roots \
  | jq -r '.crts[0]' > ~/.ai-agent-bridge/certs/step-ca-root.crt

bin/bridgectl server start \
  --listen 100.b.b.b:9445 \
  --san machine-b.tailnet-name.ts.net \
  --step-ca-url https://ca-host.tailnet-name.ts.net:9443 \
  --step-ca-root ~/.ai-agent-bridge/certs/step-ca-root.crt \
  --step-ca-provisioner bridge-jwk
```

Keep both server terminals open while testing.

## 6. Enroll Machine A as a Client of Machine B

On `machine-a`:

```bash
bin/bridgectl client init \
  --step-ca-url https://ca-host.tailnet-name.ts.net:9443 \
  --step-ca-root ~/.ai-agent-bridge/certs/step-ca-root.crt \
  --provisioner bridge-jwk \
  --name machine-a \
  --target machine-b.tailnet-name.ts.net:9445
```

This obtains a client certificate from Step CA, creates a JWT signing key, registers the JWT public key with the bridge server on `machine-b`, and stores the remote in `~/.ai-agent-bridge/remotes.json`.

Verify remote access:

```bash
bin/bridgectl session list --remote machine-b.tailnet-name.ts.net
```

## 7. Start a Remote Session from Machine A

On `machine-a`, run a provider session on `machine-b`:

```bash
go run ./examples/chat-ca \
  --remote machine-b.tailnet-name.ts.net \
  --provider codex \
  --project tailscale-demo \
  --timeout 30m \
  /home/<remote-user>/repos/bridge-demo
```

The repository path is evaluated on `machine-b`, not on `machine-a`.

## 8. Use the Web UI for Local and Remote Servers

On `machine-a`:

```bash
make web-start WEB_PORT=8080
```

Open `http://localhost:8080`.

Use the UI in two modes:

- Leave remote host blank to list, start, and attach to sessions on `machine-a`.
- Enter `machine-b.tailnet-name.ts.net` to list, start, and attach to sessions on `machine-b`.

Use different repository paths for each host. A path that exists on `machine-a` does not imply the same path exists on `machine-b`.

## 9. Stop the Test Stack

Stop each bridge server with `Ctrl-C`.

On the Step CA host:

```bash
make down-step-ca
```

To delete CA state:

```bash
docker compose -f step-ca/docker-compose.step-ca.yaml down -v
```
