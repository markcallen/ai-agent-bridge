---
title: Migrating to v1.1
---

# Migrating to v1.1 Security Model

v1.1 introduces explicit security modes and a certificate provider abstraction. **Existing configurations continue to work without changes** — legacy fields are automatically translated to the new model.

## What changed

| Before (v1.0) | After (v1.1) |
|----------------|--------------|
| `server.listen` presence determines mode | Explicit `security.transport.mode` |
| `step_ca.*` fields at top level | `security.certificates.provider: stepca` |
| `tls.*` fields for explicit certs | `security.certificates.provider: filesystem` |
| Implicit auto-PKI when no TLS config | `security.certificates.provider: auto` |
| Binary local/secure mode | Three modes: `local`, `tls`, `mtls` |

## Automatic translation

When your YAML config has no `security:` block, bridgectl synthesizes one from legacy fields:

| Legacy config | Synthesized security config |
|---------------|---------------------------|
| No `server.listen`, no `step_ca`, no `tls` | `mode: local, provider: auto, auth: none` |
| `server.listen` + `step_ca.url` | `mode: mtls, provider: stepca, auth: jwt` |
| `server.listen` + `tls.ca_bundle` | `mode: mtls, provider: filesystem, auth: jwt` |
| `server.listen` only | `mode: mtls, provider: auto, auth: jwt` |

## New configuration format

### Before (v1.0)

```yaml
server:
  listen: "0.0.0.0:9445"

step_ca:
  url: https://step-ca.example.com
  root: /etc/bridgectl/step-ca-root.crt
  provisioner: admin
  provisioner_password_file: /etc/bridgectl/pw

auth:
  jwt_public_keys:
    - issuer: "client1"
      key_path: "/etc/bridgectl/client1.pub"
```

### After (v1.1)

```yaml
server:
  listen: "0.0.0.0:9445"

security:
  transport:
    mode: mtls
  certificates:
    provider: stepca
    stepca:
      url: https://step-ca.example.com
      root: /etc/bridgectl/step-ca-root.crt
      provisioner: admin
      provisioner_password_file: /etc/bridgectl/pw
  authorization:
    mode: jwt

auth:
  jwt_public_keys:
    - issuer: "client1"
      key_path: "/etc/bridgectl/client1.pub"
```

## New CLI commands

v1.1 adds these commands:

| Command | Purpose |
|---------|---------|
| `bridgectl server start --mode tls\|mtls\|local` | Explicit security mode |
| `bridgectl enrollment create --identity NAME` | Create one-time enrollment token |
| `bridgectl identity show` | Show certificate identity details |
| `bridgectl identity renew` | Trigger immediate certificate renewal |
| `bridgectl client setup --bundle FILE` | Extract credential bundle on client |

## New TLS mode

v1.1 adds `tls` mode — server presents a TLS certificate but does not require a client certificate. JWT is the sole authentication mechanism. This is useful for trusted network deployments where mTLS certificate management overhead is not justified.

## Migration steps

1. **No action required** for existing deployments. Legacy configs continue to work.
2. **Optional**: Add a `security:` block to your config for clarity. The legacy fields will be ignored when `security:` is present.
3. **Optional**: Use `--mode tls` if your deployment doesn't need client certificates.
4. **Future**: Legacy `step_ca.*` and `tls.*` fields will be deprecated in v1.2 and removed in v2.0.
