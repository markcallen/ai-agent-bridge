---
title: Web UI Example
---

`examples/web` is a Go HTTP server plus a Vite/React frontend. The browser talks to the Go server, and the Go server connects to a local bridge server or a remote bridge host.

## Local Server Plus Web UI

Terminal 1, start the bridge server:

```bash
make build-cli
bin/bridgectl server start
```

Terminal 2, start the web UI in production mode:

```bash
make web-start WEB_PORT=8080
```

Open `http://localhost:8080`. The web server binds to all interfaces by default. On a shared network, set `WEB_HOST=127.0.0.1` to bind to loopback only.

In the web UI:

1. Leave the remote host field empty for the local server.
2. Enter a project ID such as `local-demo`.
3. Select a provider that is configured on this machine.
4. Enter a repository path allowed by the bridge config.
5. Start the session.
6. Select the session to watch output and send input.

## Development Mode

The development mode uses Vite for frontend hot reload.

```bash
# Terminal 1
cd examples/web/server
go run . --port 8080 --vite-port 5173

# Terminal 2
cd examples/web/ui
pnpm install --frozen-lockfile
pnpm dev
```

Open `http://localhost:5173`.

## Docker Compose

The repository compose files build the bridge container and the current web example.

```bash
docker compose up --build
```

Open `http://localhost:3000`.

For hot reload:

```bash
docker compose -f docker-compose.yml -f docker-compose.local.yaml up --build --watch
```

Open `http://localhost:5173`.

## Connecting to a Remote Server

Remote connections use credentials from `~/.config/bridgectl/certs` on the machine running the web server. Enroll first:

```bash
bin/bridgectl client init \
  --step-ca-url https://<step-ca-tailnet-name>:9443 \
  --provisioner bridge-jwk \
  --target <remote-bridge-tailnet-name>:9445
```

Then run the web UI locally and enter the remote bridge hostname in the remote host field. You can switch between blank local mode and named remote hosts without restarting the web UI.
