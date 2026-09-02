---
title: Using an Existing CA
---

# Using an Existing CA

bridgectl supports standard mTLS with certificates from any PKI. You do not need Step CA.

## Supported PKI systems

- Enterprise/private CAs
- AWS Private CA
- HashiCorp Vault PKI
- Kubernetes cert-manager
- SPIFFE/SPIRE
- Let's Encrypt (for server certs)
- Manually managed X.509 certificates

## Requirements

You need three PEM files:

| File | Purpose |
|------|---------|
| CA bundle | Trust root(s) for verifying peer certificates |
| Certificate | Server or client end-entity certificate |
| Private key | Matching private key (ECDSA P-384 recommended) |

## Server configuration

```yaml
security:
  transport:
    mode: mtls
  certificates:
    provider: filesystem
    filesystem:
      ca: /etc/bridgectl/ca-bundle.pem
      server_certificate: /etc/bridgectl/server.crt
      server_private_key: /etc/bridgectl/server.key
  authorization:
    mode: jwt
```

Or via CLI flags:

```bash
bridgectl server start \
  --listen 0.0.0.0:9445 \
  --mode mtls \
  --ca-bundle /etc/bridgectl/ca-bundle.pem \
  --tls-cert /etc/bridgectl/server.crt \
  --tls-key /etc/bridgectl/server.key
```

## Client credentials

Issue client credentials using the same CA that signed the server cert. bridgectl does not care how the certificates were created as long as:

1. The client cert chains to a CA in the server's trust bundle
2. The server cert chains to a CA in the client's trust bundle
3. The client cert has `ExtKeyUsage: ClientAuth`
4. The server cert has `ExtKeyUsage: ServerAuth`

After obtaining your client cert from your PKI, enroll with the bridge server:

```bash
bridgectl client enroll \
  --target bridge-host:9445 \
  --ca /path/to/ca-bundle.pem \
  --cert /path/to/client.crt \
  --key /path/to/client.key
```

## Certificate rotation

The filesystem provider does not perform automatic renewal. When your external PKI rotates certificates:

1. Write the new cert and key to the same file paths
2. bridgectl's `CertReloader` detects the file change on the next TLS handshake
3. New connections use the updated certificate
4. Existing connections are unaffected

No server restart is required.

## Validation

bridgectl validates filesystem certificates on startup:

- All three files must exist and be readable
- The certificate and key must match (same public key)
- The certificate must chain to the CA bundle
- The private key file must have restrictive permissions (0600)
