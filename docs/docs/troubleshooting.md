---
title: Troubleshooting
---

## No Server Found

Error:

```text
no bridgectl server running
```

Start the server in another terminal:

```bash
bin/bridgectl server start
```

If the server is running in secure TCP mode, check:

```bash
bin/bridgectl server status
```

## Unknown JWT Issuer

Remote clients need both an mTLS certificate and a registered JWT public key. Re-enroll:

```bash
bin/bridgectl client init \
  --step-ca-url https://ca-host.tailnet-name.ts.net:9443 \
  --target bridge-host.tailnet-name.ts.net:9445
```

If you already have a client certificate:

```bash
bin/bridgectl client enroll \
  --target bridge-host.tailnet-name.ts.net:9445 \
  --ca ~/.config/bridgectl/certs/step-ca-root.crt \
  --cert ~/.config/bridgectl/certs/<name>.crt \
  --key ~/.config/bridgectl/certs/<name>.key
```

## TLS Server Name Fails

The remote hostname must appear in the bridge server certificate SANs. Start the server with the Tailscale DNS name:

```bash
bin/bridgectl server start \
  --listen 100.x.y.z:9445 \
  --san machine.tailnet-name.ts.net
```

If you must dial by IP, pass `--server-name machine.tailnet-name.ts.net` on CLI session commands.

## Provider Unavailable

Run:

```bash
bin/bridgectl server status
```

Common causes:

- The provider binary is not on `PATH` or the configured path is wrong.
- The provider credential environment variable is missing.
- The provider startup probe timed out.
- The repository path is outside `allowed_paths`.

## Web UI Cannot Connect

For local mode, the web server must run as the same user that started `bin/bridgectl server start`.

For compose mode, check:

```bash
docker compose logs bridge
docker compose logs web
```

The web container needs `BRIDGE_ADDR`, `CA_CERT`, `CLIENT_CERT`, `CLIENT_KEY`, `JWT_KEY`, and `JWT_ISSUER` to match the bridge container's generated credentials.

## Tailscale Host Not Reachable

Check from the client machine:

```bash
tailscale status
nc -vz machine-b.tailnet-name.ts.net 9445
nc -vz ca-host.tailnet-name.ts.net 9443
```

If `nc` fails, check Tailscale ACLs, local firewalls, and whether the bridge or Step CA bound to the Tailscale IP.
