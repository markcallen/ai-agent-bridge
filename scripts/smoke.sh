#!/usr/bin/env bash
set -euo pipefail

compose=(docker compose -f docker-compose.yml -f docker-compose.smoke.yaml)

cleanup() {
  "${compose[@]}" logs bridge || true
  "${compose[@]}" down -v --remove-orphans || true
}

trap cleanup EXIT

echo "==> Building and starting smoke stack"
"${compose[@]}" up -d --build bridge

echo "==> Waiting for bridge health via gRPC"
go run ./e2e/cmd/smoke \
  -target 127.0.0.1:9445 \
  -cacert certs/ca-bundle.crt \
  -cert certs/dev-client.crt \
  -key certs/dev-client.key \
  -jwt-key certs/jwt-signing.key
