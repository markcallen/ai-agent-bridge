---
title: Certificate Rotation
---

# Certificate Rotation

## Certificate lifetime

| Provider | Default lifetime | Configurable |
|----------|-----------------|--------------|
| Auto (self-signed) | 90 days | `--cert-validity` flag |
| Step CA | Set by CA policy | CA admin controls |
| Filesystem | External PKI controls | N/A |

## Renewal timing

bridgectl renews certificates when less than **1/3 of the original lifetime** remains. For a 90-day certificate, renewal triggers at day 60 (30 days remaining).

The renewal check runs periodically:
- Default interval: 1 hour
- Configurable: `--cert-renewal-check-interval` or `server.cert_renewal_check_interval` in YAML

## How renewal works

### Auto provider

Re-issues the certificate from the local self-signed CA with the same CN and SANs:

1. Load the existing CA cert and key
2. Extract CN and SANs from the current certificate
3. Issue a new certificate with the same identity
4. Write to the same file paths (overwrite)
5. `CertReloader` picks up the new cert on the next handshake

### Step CA provider

Uses mTLS-based renewal with automatic fallback:

1. Present the existing certificate to Step CA's `/renew` endpoint
2. Step CA validates the certificate and issues a replacement
3. If mTLS renewal fails (e.g. certificate expired beyond Step CA's grace window), fall back to provisioner-based re-enrollment (JWK or ACME)
4. Write the renewed certificate to the same path
5. `CertReloader` picks up the new cert

### Filesystem provider

Renewal is external. bridgectl watches for file changes:

1. Your external PKI writes a new cert/key to the configured paths
2. On the next TLS handshake, `CertReloader` checks the file modification time
3. If changed, it reloads the certificate from disk
4. The new certificate is used for all subsequent connections

## Hot reload behavior

The `CertReloader` checks file modification time on **every TLS handshake**:

- If the file has not changed, it returns the cached certificate (zero overhead)
- If the file has changed, it reloads from disk
- If the reload fails, the previous certificate is preserved (no downtime)
- Existing connections are unaffected; only new connections use the new cert

## Manual renewal

```bash
# Show current certificate status
bridgectl identity show

# Trigger immediate renewal
bridgectl identity renew
```

## Failure handling

- Renewal failures are logged but do not crash the server
- The server continues with the existing certificate until it expires
- bridgectl **never** falls back to insecure (plaintext) transport on renewal failure
- If all renewal paths fail, the certificate will eventually expire and new connections will fail with TLS errors

## Emergency rotation

If a private key is compromised:

1. Revoke the certificate at your CA (if supported)
2. Generate a new key and certificate
3. Replace the files at the configured paths
4. bridgectl picks up the new cert on the next handshake, or restart the server

For Step CA deployments with short-lived certificates (e.g. 24 hours), certificate expiry acts as implicit revocation.
