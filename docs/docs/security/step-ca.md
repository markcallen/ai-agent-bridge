---
title: Step CA Integration
---

# Step CA Integration

[Smallstep Certificate Authority](https://smallstep.com/certificates/) is the recommended automated PKI for bridgectl deployments with multiple machines. It provides short-lived certificates, automatic renewal, OIDC-based enrollment, and cloud workload identity support.

bridgectl wraps all Step CA operations behind `bridgectl` commands. You should not normally need to run `step` commands directly.

## Server Setup

### Interactive setup

```bash
bridgectl server init
```

This auto-detects your Tailscale hostname, fetches the Step CA root certificate, and writes `~/.config/bridgectl/bridge.yaml`. Then start the server:

```bash
bridgectl server start
```

The server obtains its TLS certificate from Step CA automatically.

### Manual setup

```bash
bridgectl server start \
  --listen 0.0.0.0:9445 \
  --san myhost.example.com \
  --step-ca-url https://step-ca.example.com \
  --step-ca-root ~/.config/bridgectl/certs/step-ca-root.crt \
  --step-ca-provisioner admin
```

### Configuration file

```yaml
security:
  transport:
    mode: mtls
  certificates:
    provider: stepca
    stepca:
      url: https://step-ca.example.com
      root: /etc/bridgectl/step-ca-root.crt
      provisioner: admin
      provisioner_password_file: /etc/bridgectl/provisioner-password
  authorization:
    mode: jwt
```

## Client Setup

### Option A: JWK provisioner (password-based)

No port binding or sudo needed:

```bash
bridgectl client init \
  --step-ca-url https://step-ca.example.com \
  --provisioner admin \
  --target bridge-host.example.com:9445
```

This discovers provisioners from the CA, obtains a client certificate, and auto-enrolls with the bridge server.

### Option B: Pre-issued credentials

If the server operator issues credentials:

```bash
# Server operator (on the server machine):
bridgectl server issue-client --name remote-machine --bundle --deploy remote-machine:~/

# Client operator (on the client machine):
bridgectl client setup --bundle ~/remote-machine-creds.tar.gz
bridgectl client enroll --target bridge-host.example.com:9445
```

### Option C: OIDC provisioner (Google SSO)

For teams using Google Workspace, operators authenticate with their Google account:

```bash
bridgectl server issue-client \
  --name operator-name \
  --oidc-provider https://accounts.google.com \
  --step-ca-url https://step-ca.example.com \
  --step-ca-root ~/.config/bridgectl/certs/step-ca-root.crt
```

## Provisioners

Step CA supports multiple provisioner types:

| Provisioner | Auth method | Use case |
|-------------|-------------|----------|
| JWK (`admin`) | Shared password | CI runners, automated clients, bridge server |
| OIDC (`google`) | Google SSO browser login | Human operators |
| ACME | HTTP-01 challenge (port 80) | Automated server certs |
| GCP | Instance identity token | GCE workloads |
| AWS | Instance identity document | EC2 workloads |

## Certificate Renewal

When using the Step CA provider, bridgectl handles certificate renewal automatically:

- The server checks certificate expiry periodically (default: every hour)
- Renewal triggers when less than 1/3 of the certificate lifetime remains
- Renewal uses mTLS-based renewal (presenting the existing cert to Step CA)
- If mTLS renewal fails (e.g. cert already expired), it falls back to provisioner-based re-enrollment
- The `CertReloader` picks up renewed certificates on the next TLS handshake without restarting

Manual renewal:

```bash
bridgectl identity renew
```

## Troubleshooting

### "Step CA root does not verify the HTTPS endpoint"

The Step CA server's TLS certificate may be signed by a different CA than its own root (e.g. Let's Encrypt for the server, Step CA root for issued certs). bridgectl falls back to native token renewal in this case.

### "init JWK provisioner: connection refused"

The Step CA server is not reachable. Check:
- The URL is correct (including port)
- The Step CA server is running
- Network connectivity (Tailscale, firewall rules)

### "provisioner password file is required"

Non-interactive JWK enrollment requires `--step-ca-provisioner-password-file`. Create a file with the provisioner password:

```bash
echo -n "your-password" > /path/to/password-file
chmod 600 /path/to/password-file
```
