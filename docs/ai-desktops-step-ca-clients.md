# AI Desktops Step CA Client Startup Registry

This guide explains how `ai-desktops` should use the bridge server's
`step_ca.clients` startup registry to make existing desktop clients available
when the server starts.

## What this solves

Remote bridge clients authenticate with two credentials:

- A client TLS certificate trusted through Step CA.
- An Ed25519 JWT signing key trusted by the bridge server.

Step CA can prove that a desktop owns a valid client certificate, but it does
not automatically make that desktop's JWT signing key trusted by the bridge.
The bridge server must also know the desktop's JWT public key.

Before `step_ca.clients`, adding a pre-existing desktop often required a
manual server-side enrollment step after startup. With `step_ca.clients`, the
server can load known desktop JWT public keys during startup.

## Server configuration

Add each known desktop under `step_ca.clients`:

```yaml
step_ca:
  url: "https://step-ca.example.internal"
  root: "/etc/ai-agent-bridge/step-ca-root.crt"
  clients:
    - issuer: "desktop-mark"
      key_path: "/var/lib/ai-agent-bridge/certs/jwt-clients/desktop-mark.pub"
      required: true
    - issuer: "desktop-ci"
      required: false
```

Fields:

| Field | Required | Description |
|-------|----------|-------------|
| `issuer` | Yes | JWT issuer name for the desktop. For `bridgectl` remote clients, this is normally the client certificate common name. |
| `key_path` | No | Path to the desktop's Ed25519 JWT public key on the server. |
| `required` | No | When `true`, server startup fails if the key cannot be loaded. When omitted or `false`, the server logs a warning and skips the desktop if the key is unavailable. |

If `key_path` is omitted, the server checks this default path under its state
directory:

```text
certs/jwt-clients/<issuer>.pub
```

For a packaged Linux install, that usually means:

```text
/var/lib/ai-agent-bridge/certs/jwt-clients/<issuer>.pub
```

## Desktop-side expectations

Each `ai-desktops` machine needs:

- A Step CA-issued client certificate and key.
- A local JWT private key, normally `jwt-signing.key`.
- A matching JWT public key copied to the bridge server.

The issuer configured on the server must match the issuer the desktop uses
when minting JWTs. With `bridgectl --remote`, the issuer is derived from the
client certificate common name, so use the desktop's certificate CN as the
`step_ca.clients[].issuer` value.

Example desktop identity:

```text
client certificate CN: desktop-mark
JWT private key:       ~/.ai-agent-bridge/certs/jwt-signing.key
server public key:     /var/lib/ai-agent-bridge/certs/jwt-clients/desktop-mark.pub
server config issuer:  desktop-mark
```

## Recommended ai-desktops workflow

1. Pick a stable desktop issuer name, such as `desktop-mark` or
   `desktop-build-01`.

2. Enroll the desktop with Step CA so its client certificate CN matches the
   issuer name.

3. Generate or reuse the desktop's Ed25519 JWT keypair.

4. Copy only the `.pub` file to the bridge server:

   ```bash
   sudo install -d -m 0755 /var/lib/ai-agent-bridge/certs/jwt-clients
   sudo install -m 0644 desktop-mark.pub \
     /var/lib/ai-agent-bridge/certs/jwt-clients/desktop-mark.pub
   ```

5. Add the desktop to the server config:

   ```yaml
   step_ca:
     clients:
       - issuer: "desktop-mark"
         required: true
   ```

6. Start or restart the bridge server.

7. From the desktop, connect normally:

   ```bash
   bridgectl session list --remote bridge.example.internal:9445
   ```

## Optional versus required clients

Use `required: true` for desktops that must be trusted before the server is
considered healthy. This is appropriate for primary operator workstations or
automation nodes that should always be present.

Use `required: false` or omit the field for desktops that may not have finished
enrollment yet. If their public key is missing, unreadable, or invalid, the
server starts and logs a warning. The desktop will not be able to authenticate
until its public key is installed and the server is started again.

## Important security notes

- Never copy a desktop's JWT private key to the bridge server. The server only
  needs the `.pub` file.
- Step CA trust and JWT trust are separate. A valid Step CA certificate is not
  enough to call bridge RPCs unless the JWT issuer is also trusted.
- Keep issuer names stable. If the desktop certificate CN changes, update the
  matching `step_ca.clients[].issuer` entry and public key filename.
- Avoid sharing one JWT keypair across desktops. Each desktop should have its
  own issuer and keypair so access can be revoked independently.

## Troubleshooting

If the desktop gets `UNAUTHENTICATED`, check:

- The desktop certificate is valid and chains to the Step CA root trusted by
  the server.
- The desktop's JWT issuer matches `step_ca.clients[].issuer`.
- The server has the matching JWT public key at `key_path` or the default
  `certs/jwt-clients/<issuer>.pub` path.
- The public key file contains the Ed25519 public key, not the private key.
- The server was started after the public key was installed.

If startup fails for a configured desktop, check whether that entry is marked
`required: true`. Required clients fail startup when their public key is
missing, unreadable, or invalid.
