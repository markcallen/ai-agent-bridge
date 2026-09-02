---
title: Security Overview
---

# Security Overview

> **Secure by default. Bring your own identity.**

bridgectl uses mTLS for workload identity and JWT for authorization. You can use your existing PKI or the built-in Step CA integration for automated certificate lifecycle management.

## Security Modes

bridgectl supports three explicit transport security modes:

| Mode | Transport | Identity | Use case |
|------|-----------|----------|----------|
| `local` | Plaintext (loopback) | None | Development, single machine |
| `tls` | Server TLS | JWT | Trusted private networks |
| `mtls` | Mutual TLS | Client certificate + JWT | Production, zero-trust |

### Local mode

Default for `bridgectl run`. The server listens on a Unix domain socket with no authentication. Suitable for single-developer use on a local machine.

### TLS mode

The server presents a TLS certificate to clients. Clients verify the server but do not present a certificate themselves. JWT tokens provide authorization.

```bash
bridgectl server start --listen 0.0.0.0:9445 --mode tls
```

### mTLS mode

Both server and client present X.509 certificates. JWT tokens provide fine-grained authorization on top of the transport identity.

```bash
bridgectl server start --listen 0.0.0.0:9445 --mode mtls
```

## Identity Model

bridgectl separates transport identity from authorization:

```
mTLS
"Who is this machine or service?"

JWT
"What is this caller allowed to do?"
```

mTLS establishes workload/machine identity. A certificate's Common Name (CN) identifies the machine or service. JWT provides application-level authorization — which projects and sessions the caller can access.

## Certificate Providers

Certificate material can come from any source that produces standard X.509 certificates:

```
certificate providers
├── auto        (self-signed, default)
├── filesystem  (bring your own certs)
├── stepca      (automated enrollment and renewal)
├── spiffe      (future)
├── vault       (future)
└── cert-manager (future)
```

### Auto provider

The default. bridgectl generates a self-signed CA and issues server/client certificates automatically. No external dependencies. Suitable for development and single-operator deployments.

### Filesystem provider

Reads certificates and keys from files on disk. Use this when your organization already manages X.509 certificates through an external PKI (enterprise CA, Let's Encrypt, AWS Private CA, etc.).

```yaml
security:
  transport:
    mode: mtls
  certificates:
    provider: filesystem
    filesystem:
      ca: /etc/bridgectl/ca.pem
      certificate: /etc/bridgectl/client.pem
      private_key: /etc/bridgectl/client-key.pem
  authorization:
    mode: jwt
```

### Step CA provider

Integrates with [Smallstep Certificate Authority](https://smallstep.com/certificates/) for automated certificate enrollment, short-lived certificates, and renewal. See [Step CA Integration](step-ca.md) for details.

## Configuration

The security model is configured in `bridge.yaml`:

```yaml
# Production: mTLS + JWT + Step CA
security:
  transport:
    mode: mtls
  certificates:
    provider: stepca
  authorization:
    mode: jwt

# Trusted network: TLS + JWT
security:
  transport:
    mode: tls
  certificates:
    provider: auto
  authorization:
    mode: jwt

# Development: no auth
security:
  transport:
    mode: local
  authorization:
    mode: none
```

Legacy configuration (using `server.listen`, `tls.*`, `step_ca.*` fields) continues to work and is automatically translated to the new model.

## Threat Model

bridgectl's security model protects against:

- **Unauthorized session access**: mTLS ensures only machines with trusted certificates can connect. JWT scopes restrict which projects and sessions a caller can access.
- **Eavesdropping**: TLS 1.3 minimum for all secure modes.
- **Credential theft**: Private keys are generated locally and never transmitted. Short-lived certificates (when using Step CA) limit the impact of a compromised key.
- **Replay attacks**: JWT tokens have configurable TTL (default 5 minutes) and audience validation.

bridgectl does **not** protect against:

- Compromised host machines (if the machine is compromised, the agent session is compromised)
- Denial of service (rate limiting is configured but not designed for hostile network environments)
- Side-channel attacks on the AI agent process itself
