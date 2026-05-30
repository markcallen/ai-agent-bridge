#!/usr/bin/env bash
set -euo pipefail

# Run the ai-agent-bridge-cli e2e tests inside a Linux Docker container.
# Usage: ./scripts/test-cli-e2e-docker.sh

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "=== Building and running ai-agent-bridge-cli e2e tests in Docker ==="

set +e
docker compose -f "$ROOT_DIR/e2e/ai-agent-bridge-cli/docker-compose.yml" up \
    --build \
    --abort-on-container-exit \
    --exit-code-from cli-e2e
rc=$?
docker compose -f "$ROOT_DIR/e2e/ai-agent-bridge-cli/docker-compose.yml" down -v
set -e

if [ $rc -eq 0 ]; then
    echo ""
    echo "=== CLI E2E DOCKER TESTS PASSED ==="
else
    echo ""
    echo "=== CLI E2E DOCKER TESTS FAILED (exit code $rc) ==="
fi
exit $rc
