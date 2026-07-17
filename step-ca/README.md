# Step CA Integration

This directory contains everything needed to run a [Smallstep Certificate Authority](https://smallstep.com/certificates/) alongside the AI Agent Bridge for Tier 2 PKI.

## Overview

The bridge supports two PKI tiers:

| Tier | CA Source | Use Case |
|------|----------|----------|
| **Tier 1** (default) | Auto-generated self-signed CA | Single developer, homelab |
| **Tier 2** (Step CA) | External Step CA instance | Teams with OIDC, multi-operator |

This setup provides a local Step CA for development and testing of Tier 2 features. For production deployments, see [Deployment Strategy](#deployment-strategy) below.

## Quick Start

### Prerequisites

- Docker and Docker Compose v2
- The `step` CLI on your host (optional, for manual certificate operations)
  ```bash
  # macOS
  brew install step

  # Linux
  curl -fsSL https://dl.smallstep.com/cli/install-step-cli.sh | bash
  ```

### Start Step CA + Bridge

```bash
make up-step-ca
```

This starts three containers:

1. **step-ca-init** — bootstraps the CA (runs once, then exits)
2. **step-ca** — the certificate authority server on port 9443
3. **bridge** — the AI Agent Bridge on port 9445, configured to use Step CA

### Verify

```bash
make step-ca-health
```

Should print `ok`.

### Stop

```bash
make down-step-ca
```

To remove all Step CA state (certificates, keys, configuration):

```bash
docker compose -f step-ca/docker-compose.step-ca.yaml down -v
```

## Architecture

```
┌─────────────┐         ┌─────────────┐
│  step-ca    │◄────────│   bridge    │
│  :9443      │  step   │   :9445    │
│             │  ca     │             │
│  JWK prov.  │  cert   │  mTLS+JWT  │
└─────────────┘         └──────┬──────┘
       │                       │
  (shared vol)            (login session)
  root_ca.crt                  │
                        ┌──────▼──────┐
                        │  AI Agent   │
                        │  (claude,   │
                        │   codex...) │
                        └─────────────┘
```

### What happens on startup

1. `step-ca-init` runs `step ca init` to create:
   - A root CA and intermediate CA
   - A JWK provisioner named `bridge-jwk` (password-based, for automated cert issuance)
   - Exports the root cert to a shared volume

2. `step-ca` starts the CA server, trusting the init output

3. The bridge container:
   - Reads the Step CA root cert from the shared volume
   - Starts `bridgectl server start --step-ca-url ... --step-ca-root ...`
   - `EnsurePKI()` calls `step ca certificate` to obtain a server cert from Step CA
   - Creates a local CA for CLI credentials (so `bridgectl` can manage the server)
   - Appends the local CA to the trust bundle

### Certificate flow

```
Step CA Root
    │
    ├── server.crt (issued by Step CA, 90-day validity)
    │
Local CA (auto-generated)
    │
    ├── local-client.crt (for bridgectl CLI)
    ├── clients/<name>/<name>.crt (per-client, via issue-client)
    │
Trust Bundle (ca-bundle.crt)
    └── Contains: Step CA root + Local CA
```

## Remote Client Enrollment (Tier 2)

With Step CA, remote clients get their own certificates directly from the CA — no file copying from the server required.

### 1. Start the bridge server

On the server host (e.g. your macbook), point bridgectl at the Step CA instance.
The `step` CLI will prompt for the provisioner password interactively:

```bash
# Save the Step CA root cert
curl -sk https://step-ca-dev.example.com/roots | jq -r '.crts[0]' > step-ca-root.crt

# Start the server (step CLI prompts for provisioner password)
bridgectl server start \
  --listen 0.0.0.0:9445 \
  --san myhost.example.com \
  --step-ca-url https://step-ca-dev.example.com \
  --step-ca-root step-ca-root.crt
```

> For headless or Docker environments where interactive prompts are not
> available, pass `--step-ca-provisioner-password-file /path/to/pw.txt`.

### 2. Enroll a remote client

On the remote client machine (e.g. a dev server), get a certificate from Step CA
and enroll with the bridge server. No files need to be copied from the server:

```bash
# a. Get the Step CA root cert
curl -sk https://step-ca-dev.example.com/roots | jq -r '.crts[0]' > step-ca-root.crt

# b. Get a client certificate from Step CA (prompts for provisioner password)
step ca certificate my-client my-client.crt my-client.key \
  --ca-url https://step-ca-dev.example.com \
  --root step-ca-root.crt

# c. Enroll with the bridge server (generates JWT keypair and registers it)
bridgectl client enroll \
  --target myhost.example.com:9445 \
  --ca step-ca-root.crt \
  --cert my-client.crt \
  --key my-client.key
```

After enrollment, the client has everything it needs to connect with mTLS + JWT.

### 3. Connect with the Go SDK

```go
client, _ := bridgeclient.New(
    bridgeclient.WithTarget("myhost.example.com:9445"),
    bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
        CABundlePath: "step-ca-root.crt",
        CertPath:     "my-client.crt",
        KeyPath:      "my-client.key",
        ServerName:   "server",
    }),
    bridgeclient.WithJWT(bridgeclient.JWTConfig{
        PrivateKeyPath: "jwt-signing.key",  // generated by enroll
        Issuer:         "my-client",
        Audience:       "bridge",
        TTL:            5 * time.Minute,
    }),
)
```

## Testing with Docker Compose

The Docker Compose setup runs Step CA and the bridge together for local testing.

### 1. Issue a client certificate (local CA path)

Even with Step CA enabled, local CA client issuance works for testing:

```bash
make step-ca-issue-client STEP_CA_CLIENT_NAME=test-client
```

Note: `bridgectl server issue-client` must run inside the bridge container
because it needs the CA private key to sign the certificate. The CA key
stays on the server by design — it should never leave the host.

To extract the credentials from the container:

```bash
CLIENT=test-client
COMPOSE="docker compose -f step-ca/docker-compose.step-ca.yaml"
CERTS=/home/bridge/.ai-agent-bridge/certs
mkdir -p /tmp/bridge-creds

$COMPOSE exec bridge cat $CERTS/ca-bundle.crt              > /tmp/bridge-creds/ca-bundle.crt
$COMPOSE exec bridge cat $CERTS/clients/$CLIENT/$CLIENT.crt > /tmp/bridge-creds/$CLIENT.crt
$COMPOSE exec bridge cat $CERTS/clients/$CLIENT/$CLIENT.key > /tmp/bridge-creds/$CLIENT.key
```

Then enroll the client's JWT key:

```bash
bridgectl client enroll \
  --target localhost:9445 \
  --ca /tmp/bridge-creds/ca-bundle.crt \
  --cert /tmp/bridge-creds/$CLIENT.crt \
  --key /tmp/bridge-creds/$CLIENT.key
```

### 2. Issue a certificate directly from Step CA

```bash
# Get the root cert
docker compose -f step-ca/docker-compose.step-ca.yaml exec step-ca \
  cat /home/step/certs/root_ca.crt > /tmp/step-root.crt

# Issue a cert (uses the JWK provisioner, prompts for password)
step ca certificate my-sdk /tmp/my-sdk.crt /tmp/my-sdk.key \
  --ca-url https://localhost:9443 \
  --root /tmp/step-root.crt \
  --provisioner bridge-jwk \
  --not-after 24h
# Password: step-ca-dev-password (or the value of STEP_CA_PASSWORD)
```

### 3. Test OIDC enrollment (requires OIDC provisioner, optional)

To test OIDC-based client enrollment, add an OIDC provisioner to Step CA:

```bash
# Add Google OIDC provisioner
docker compose -f step-ca/docker-compose.step-ca.yaml exec step-ca \
  step ca provisioner add google \
  --type OIDC \
  --client-id YOUR_GOOGLE_CLIENT_ID \
  --client-secret YOUR_GOOGLE_CLIENT_SECRET \
  --configuration-endpoint https://accounts.google.com/.well-known/openid-configuration \
  --admin-password-file /home/step/secrets/password

# Then issue a client cert via OIDC
docker compose -f step-ca/docker-compose.step-ca.yaml exec bridge \
  su -s /bin/bash bridge -c \
  'HOME=/home/bridge bridgectl server issue-client \
    --name mark \
    --oidc-provider https://accounts.google.com \
    --step-ca-url https://step-ca.local:9443 \
    --step-ca-root /step-ca/root_ca.crt'
```

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `STEP_CA_PASSWORD` | `step-ca-dev-password` | JWK provisioner password |
| `STEP_CA_URL` | (set by compose) | Step CA server URL |
| `STEP_CA_ROOT` | (set by compose) | Path to Step CA root cert |
| `STEP_CA_PROVISIONER_PASSWORD` | (set by compose) | Provisioner password for bridge |

### Custom password

```bash
STEP_CA_PASSWORD=my-secret-password make up-step-ca
```

### Reset Step CA state

```bash
make down-step-ca
docker volume rm step-ca_step-ca-data step-ca_step-ca-shared 2>/dev/null || true
make up-step-ca
```

## Deployment Strategy

### Tier 1: Local Development (this setup)

- **When**: Single developer, homelab, quick start
- **How**: `make up-step-ca` — Docker Compose with JWK provisioner
- **Root cert**: Self-signed, trusted only by containers in the compose network
- **Provisioner**: JWK (password-based)
- **Certificate renewal**: Manual (re-run `make down-step-ca && make up-step-ca`)

### Tier 2: Team / Staging

- **When**: Multiple developers sharing a bridge instance over VPN
- **How**: Step CA on a dedicated VM or container behind WireGuard/Tailscale
- **Root cert**: Distributed via VPN provisioning or `step ca bootstrap`
- **Provisioners**:
  - **JWK** for automated SDK clients (CI runners, orchestrators)
  - **OIDC** for human operators (Google Workspace, GitHub, Okta, Auth0)
- **Certificate renewal**: Automatic via `step ca renew` cron or systemd timer
- **Setup**:
  ```bash
  # On the Step CA host
  step ca init --deployment-type standalone \
    --name "Team Bridge CA" \
    --dns step-ca.vpn.internal \
    --address :443

  # Add OIDC provisioner
  step ca provisioner add google --type OIDC \
    --client-id $GOOGLE_CLIENT_ID \
    --client-secret $GOOGLE_CLIENT_SECRET \
    --configuration-endpoint https://accounts.google.com/.well-known/openid-configuration

  # On each bridge host — fetch root cert and start the server
  curl -sk https://step-ca.vpn.internal/roots | jq -r '.crts[0]' > step-ca-root.crt
  bridgectl server start --listen 0.0.0.0:9445 \
    --san $(hostname).vpn.internal \
    --step-ca-url https://step-ca.vpn.internal:443 \
    --step-ca-root step-ca-root.crt

  # On each client — get a cert from Step CA and enroll with the bridge
  curl -sk https://step-ca.vpn.internal/roots | jq -r '.crts[0]' > step-ca-root.crt
  step ca certificate my-client my-client.crt my-client.key \
    --ca-url https://step-ca.vpn.internal:443 \
    --root step-ca-root.crt
  bridgectl client enroll \
    --target bridge-host.vpn.internal:9445 \
    --ca step-ca-root.crt \
    --cert my-client.crt \
    --key my-client.key
  ```

### Tier 3: Production

- **When**: Multi-team, compliance requirements, audited access
- **How**: Step CA with HA backend (PostgreSQL or MySQL), HSM for root key
- **Root cert**: Offline root CA, online intermediate (two-tier PKI)
- **Provisioners**:
  - **OIDC** for all human access (SSO-gated)
  - **JWK** or **ACME** for automated systems
  - **X5C** for cross-domain trust with external CAs
- **Certificate renewal**: Automatic via ACME or `step ca renew --daemon`
- **Monitoring**: Step CA exposes Prometheus metrics at `/metrics`
- **Key management**: YubiKey, AWS CloudHSM, or Google Cloud KMS for root key
- **Revocation**: CRL or short-lived certificates (24h) with no revocation needed
- **Reference**: [Smallstep Production Considerations](https://smallstep.com/docs/step-ca/certificate-authority-server-production/)
