#!/usr/bin/env bash
set -euo pipefail

# Usage: smoke-deb-docker.sh <path-to-.deb> <suite>
# Suite must be one of: noble, plucky

DEB_PATH="${1:-}"
SUITE="${2:-}"

if [[ -z "$DEB_PATH" || -z "$SUITE" ]]; then
  echo "Usage: $0 <path-to-.deb> <suite>" >&2
  exit 1
fi

if [[ ! -f "$DEB_PATH" ]]; then
  echo "smoke-deb-docker: deb not found: $DEB_PATH" >&2
  exit 1
fi

DEB_PATH="$(realpath "$DEB_PATH")"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
HEALTHCHECK_BIN="$TMP_DIR/plain-healthcheck"
CONTAINER="deb-smoke-$SUITE-$$"

: "${GOCACHE:=/tmp/ai-agent-bridge-go-build}"
: "${GOFLAGS:=-buildvcs=false}"
export GOCACHE GOFLAGS

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

case "$SUITE" in
  noble)  IMAGE="ubuntu:24.04" ;;
  plucky) IMAGE="ubuntu:25.04" ;;
  *)
    echo "DEB SMOKE FAILED: unsupported suite=$SUITE" >&2
    exit 1
    ;;
esac

DEB_BASENAME="$(basename "$DEB_PATH")"

go build -o "$HEALTHCHECK_BIN" "$ROOT_DIR/e2e/cmd/plain-healthcheck"

docker run -d \
  --name "$CONTAINER" \
  -v "$DEB_PATH:/tmp/$DEB_BASENAME:ro" \
  -v "$HEALTHCHECK_BIN:/usr/local/bin/plain-healthcheck:ro" \
  "$IMAGE" \
  bash -lc "
    set -euo pipefail
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    dpkg -i /tmp/$DEB_BASENAME || true
    apt-get install -f -y -qq
    /usr/bin/bridgectl server start --config /etc/ai-agent-bridge/bridge.yaml >/tmp/bridge.log 2>&1 &
    bridge_pid=\$!
    for i in \$(seq 1 15); do
      if ! kill -0 \"\$bridge_pid\" 2>/dev/null; then
        cat /tmp/bridge.log >&2
        exit 1
      fi
      sleep 1
    done
    tail -f /dev/null
  " >/dev/null

for _ in $(seq 1 30); do
  if docker exec "$CONTAINER" /usr/local/bin/plain-healthcheck >/dev/null 2>&1; then
    echo "DEB SMOKE PASSED: suite=$SUITE"
    exit 0
  fi
  sleep 1
done

docker exec "$CONTAINER" sh -c 'cat /tmp/bridge.log' >&2 || true
docker logs "$CONTAINER" >&2 || true
echo "DEB SMOKE FAILED: suite=$SUITE" >&2
exit 1
