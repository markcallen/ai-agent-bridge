---
title: Docker Compose
---

The root compose files are for local development and examples.

## Production-Style Local Compose

```bash
docker compose up --build
```

Services:

| Service | Purpose |
| --- | --- |
| `bridge` | Builds the root Dockerfile and runs `bridgectl server start`. |
| `web` | Builds `examples/web/Dockerfile` and serves the built React UI and Go API on port `3000`. |

The bridge mounts:

- `./certs` to persist local CA material
- `./config/bridge-docker.yaml`
- `${HOME}/repos` as `/repos`

The web service uses `BRIDGE_ADDR=bridge.local:9445` and the certificates generated under `./certs`.

## Hot Reload Compose

```bash
docker compose -f docker-compose.yml -f docker-compose.local.yaml up --build --watch
```

Services:

| Service | Purpose |
| --- | --- |
| `web` | Runs the Go API server in development mode on port `3000`. |
| `vite` | Runs the Vite dev server on port `5173`. |

Open `http://localhost:5173` for hot reload.

## Step CA Compose

```bash
STEP_CA_SANS=ca-host.tailnet-name.ts.net make up-step-ca
```

The Step CA compose file under `step-ca/` starts:

- `step-ca-init`
- `step-ca`
- `bridge`

For multi-machine Tailscale examples, bind `STEP_CA_BIND` and `BRIDGE_BIND` to Tailscale IPs instead of public interfaces.
