#!/usr/bin/env bash
set -euo pipefail

# Run the bridgectl e2e tests inside a Linux Docker container.
# Usage: ./scripts/test-cli-e2e-docker.sh

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

COMPOSE_FILE="$ROOT_DIR/e2e/bridgectl/docker-compose.yml"

cleanup() {
  docker compose -f "$COMPOSE_FILE" down -v 2>/dev/null
}
trap cleanup EXIT INT TERM

echo "=== Building and running bridgectl e2e tests in Docker ==="

set +e
docker compose -f "$COMPOSE_FILE" up \
    --build \
    --abort-on-container-exit \
    --exit-code-from cli-e2e
rc=$?
set -e

if [ $rc -eq 0 ]; then
    echo ""
    echo "=== CLI E2E DOCKER TESTS PASSED ==="
else
    echo ""
    echo "=== CLI E2E DOCKER TESTS FAILED (exit code $rc) ==="
fi
exit $rc
