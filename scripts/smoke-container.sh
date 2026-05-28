#!/usr/bin/env bash
set -euo pipefail

# Usage: smoke-container.sh <image>
# Pulls the given image, starts it with the CI smoke config (no TLS, no auth),
# and verifies the health endpoint responds.
#
# Example:
#   ./scripts/smoke-container.sh ghcr.io/markcallen/ai-agent-bridge:v1.2.3
#   ./scripts/smoke-container.sh ghcr.io/markcallen/ai-agent-bridge@sha256:abc123

IMAGE="${1:-}"

if [[ -z "$IMAGE" ]]; then
  echo "Usage: $0 <image>" >&2
  exit 1
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
HEALTHCHECK_BIN="$TMP_DIR/plain-healthcheck"
CONTAINER="container-smoke-$$"

: "${GOCACHE:=/tmp/ai-agent-bridge-go-build}"
: "${GOFLAGS:=-buildvcs=false}"
export GOCACHE GOFLAGS

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

go build -o "$HEALTHCHECK_BIN" "$ROOT_DIR/e2e/cmd/plain-healthcheck"

docker run -d \
  --name "$CONTAINER" \
  -p 9445 \
  -e BRIDGE_CONFIG=/app/config/bridge-ci-smoke.yaml \
  -v "$ROOT_DIR/config/bridge-ci-smoke.yaml:/app/config/bridge-ci-smoke.yaml:ro" \
  -v "$HEALTHCHECK_BIN:/usr/local/bin/plain-healthcheck:ro" \
  "$IMAGE"

MAPPED_PORT="$(docker inspect "$CONTAINER" --format '{{(index (index .NetworkSettings.Ports "9445/tcp") 0).HostPort}}')"

for _ in $(seq 1 30); do
  if "$HEALTHCHECK_BIN" -target "127.0.0.1:$MAPPED_PORT" >/dev/null 2>&1; then
    echo "CONTAINER SMOKE PASSED: image=$IMAGE"
    exit 0
  fi
  sleep 2
done

docker logs "$CONTAINER" >&2 || true
echo "CONTAINER SMOKE FAILED: image=$IMAGE" >&2
exit 1
