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

## Provisioners

Step CA supports multiple provisioner types for issuing certificates. Each
provisioner authenticates the requester differently:

| Provisioner | Type | Auth Method | Use Case |
|-------------|------|-------------|----------|
| `admin` | JWK | Shared password | CI runners, automated clients, bridge server |
| `google` | OIDC | Google SSO browser login | Human operators with Google Workspace |
| `acme` | ACME | HTTP-01 challenge (port 80) | Automated server certs |
| `google-cloud` | GCP | Instance identity token | GCE workloads |
| `amazon-web-services` | AWS | Instance identity document | EC2 workloads |

## Server Setup

### Interactive setup

```bash
bridgectl server init
```

This auto-detects your Tailscale hostname, fetches the Step CA root cert,
and writes `~/.bridgectl/bridge.yaml`. Then start the server:

```bash
bridgectl server start
```

The server obtains its TLS certificate from Step CA automatically using the
provisioner configured during init (ACME by default, JWK if specified).

### Manual setup

```bash
# Fetch the Step CA root cert
mkdir -p ~/.bridgectl/certs
curl -sk https://step-ca-dev.example.com/roots | \
  jq -r '.crts[0]' > ~/.bridgectl/certs/step-ca-root.crt

# Start the server with ACME provisioner
bridgectl server start \
  --listen 0.0.0.0:9445 \
  --san myhost.example.com \
  --step-ca-url https://step-ca-dev.example.com \
  --step-ca-root ~/.bridgectl/certs/step-ca-root.crt \
  --step-ca-provisioner acme
```

## Client Setup

### Option A: JWK provisioner (recommended for dev machines)

No port binding or sudo needed — just a password prompt:

```bash
bridgectl client init \
  --step-ca-url https://step-ca-dev.example.com \
  --target bridge-host.example.com:9445
```

This discovers available provisioners from the CA, lets you pick one
(choose the JWK provisioner, e.g. `admin`), prompts for the provisioner
password, obtains a client certificate, and auto-enrolls with the bridge
server.

Alternatively, specify the provisioner directly:

```bash
bridgectl client init \
  --step-ca-url https://step-ca-dev.example.com \
  --provisioner admin \
  --target bridge-host.example.com:9445
```

### Option B: OIDC provisioner (Google SSO)

For teams using Google Workspace, operators can authenticate with their
Google account — no shared passwords needed. This requires the `step` CLI:

```bash
# Install step CLI if not already present
# macOS: brew install step
# Linux: curl -fsSL https://dl.smallstep.com/cli/install-step-cli.sh | bash

# Fetch root cert
mkdir -p ~/.bridgectl/certs
curl -sk https://step-ca-dev.example.com/roots | \
  jq -r '.crts[0]' > ~/.bridgectl/certs/step-ca-root.crt

# Get a client cert via Google OIDC (opens browser for login)
step ca certificate my-name \
  ~/.bridgectl/certs/my-name.crt \
  ~/.bridgectl/certs/my-name.key \
  --ca-url https://step-ca-dev.example.com \
  --root /etc/ssl/certs/ca-certificates.crt \
  --provisioner google

# Enroll with the bridge server
bridgectl client enroll \
  --target bridge-host.example.com:9445 \
  --ca ~/.bridgectl/certs/step-ca-root.crt \
  --cert ~/.bridgectl/certs/my-name.crt \
  --key ~/.bridgectl/certs/my-name.key
```

> **Note**: The `--root` for the `step` CLI points to the system CA bundle
> (not the Step CA root) because the Step CA server's TLS certificate may
> be issued by a public CA like Let's Encrypt.

### After enrollment

Both options produce the same result — a client certificate and JWT keypair
in `~/.bridgectl/certs/`. Connect with the Go SDK:

```go
client, _ := bridgeclient.New(
    bridgeclient.WithTarget("bridge-host.example.com:9445"),
    bridgeclient.WithMTLS(bridgeclient.MTLSConfig{
        CABundlePath: "~/.bridgectl/certs/step-ca-root.crt",
        CertPath:     "~/.bridgectl/certs/my-name.crt",
        KeyPath:      "~/.bridgectl/certs/my-name.key",
        ServerName:   "server",
    }),
    bridgeclient.WithJWT(bridgeclient.JWTConfig{
        PrivateKeyPath: "jwt-signing.key",  // generated by enroll
        Issuer:         "my-name",
        Audience:       "bridge",
        TTL:            5 * time.Minute,
    }),
)
```

## Testing with Docker Compose

The Docker Compose setup runs Step CA and the bridge together for local testing.

### Issue a client certificate (local CA path)

Even with Step CA enabled, local CA client issuance works for testing:

```bash
make step-ca-issue-client STEP_CA_CLIENT_NAME=test-client
```

Note: `bridgectl server issue-client` must run inside the bridge container
because it needs the CA private key to sign the certificate.

To extract and enroll:

```bash
CLIENT=test-client
COMPOSE="docker compose -f step-ca/docker-compose.step-ca.yaml"
CERTS=/home/bridge/.bridgectl/certs
mkdir -p /tmp/bridge-creds

$COMPOSE exec bridge cat $CERTS/ca-bundle.crt              > /tmp/bridge-creds/ca-bundle.crt
$COMPOSE exec bridge cat $CERTS/clients/$CLIENT/$CLIENT.crt > /tmp/bridge-creds/$CLIENT.crt
$COMPOSE exec bridge cat $CERTS/clients/$CLIENT/$CLIENT.key > /tmp/bridge-creds/$CLIENT.key

bridgectl client enroll \
  --target localhost:9445 \
  --ca /tmp/bridge-creds/ca-bundle.crt \
  --cert /tmp/bridge-creds/$CLIENT.crt \
  --key /tmp/bridge-creds/$CLIENT.key
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

- **When**: Multiple developers sharing a bridge instance over Tailscale/WireGuard
- **How**: Step CA on a dedicated VM or container accessible over the VPN
- **Root cert**: Auto-fetched by `bridgectl server init` and `bridgectl client init`
- **Provisioners**:
  - **JWK** (`admin`) for automated clients, CI runners, and bridge server certs
  - **OIDC** (`google`) for human operators via Google Workspace SSO
  - **ACME** (`acme`) for automated server certs without shared passwords
  - **GCP** / **AWS** for cloud workloads using instance identity
- **Certificate renewal**: Automatic via `step ca renew` cron or systemd timer
- **Setup**:
  ```bash
  # On each bridge host
  bridgectl server init    # auto-detects Tailscale, fetches root cert
  bridgectl server start

  # On each developer machine (JWK — password, no sudo)
  bridgectl client init \
    --step-ca-url https://step-ca.vpn.internal \
    --provisioner admin \
    --target bridge-host.vpn.internal:9445

  # On each developer machine (OIDC — Google SSO, no password)
  step ca certificate my-name ~/.bridgectl/certs/my-name.crt \
    ~/.bridgectl/certs/my-name.key \
    --ca-url https://step-ca.vpn.internal \
    --root /etc/ssl/certs/ca-certificates.crt \
    --provisioner google
  bridgectl client enroll \
    --target bridge-host.vpn.internal:9445 \
    --ca ~/.bridgectl/certs/step-ca-root.crt \
    --cert ~/.bridgectl/certs/my-name.crt \
    --key ~/.bridgectl/certs/my-name.key
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
