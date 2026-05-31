#!/usr/bin/env bash
set -euo pipefail

CERT_DIR=/certs

echo "==> Waiting for certificates..."
for i in $(seq 1 60); do
  if [ -f "$CERT_DIR/ca-bundle.crt" ] && [ -f "$CERT_DIR/e2e-client.crt" ]; then
    echo "    Certificates found."
    break
  fi
  if [ "$i" -eq 60 ]; then
    echo "ERROR: timed out waiting for certificates" >&2
    exit 1
  fi
  sleep 1
done

echo "==> Preparing test repository in shared volume..."
if [ -d "/tmp/ai-agent-bridge/.git" ]; then
  echo "    Repo already present, pulling latest main..."
  git -C /tmp/ai-agent-bridge pull origin main
else
  git clone --depth 1 https://github.com/markcallen/cache-cleaner /tmp/ai-agent-bridge-src
  cp -a /tmp/ai-agent-bridge-src/. /tmp/ai-agent-bridge/
  rm -rf /tmp/ai-agent-bridge-src
fi

echo "==> Running e2e test suite..."

# Map the optional E2E_ONLY env var to a -test.run filter.
# E2E_ONLY=claude  → -test.run TestBridgeSuite/TestClaude
# E2E_ONLY=all / unset → run all suite tests
run_filter=""
if [ -n "${E2E_ONLY:-}" ] && [ "${E2E_ONLY}" != "all" ]; then
  # Capitalise first letter to match Go test method names (claude → Claude, echo → Echo)
  provider="$(echo "${E2E_ONLY}" | sed 's/\(.\)/\u\1/')"
  run_filter="-test.run TestBridgeSuite/Test${provider}"
fi

e2e-suite \
  -test.v \
  -test.timeout 300s \
  ${run_filter} \
  -bridge.target bridge:9445 \
  -bridge.cacert "$CERT_DIR/ca-bundle.crt" \
  -bridge.cert "$CERT_DIR/e2e-client.crt" \
  -bridge.key "$CERT_DIR/e2e-client.key" \
  -bridge.jwt-key "$CERT_DIR/jwt-signing.key" \
  -bridge.jwt-issuer e2e \
  -bridge.repo /tmp/ai-agent-bridge \
  -bridge.timeout 300s

exit_code=$?

if [ $exit_code -eq 0 ]; then
  echo "==> E2E test suite PASSED"
else
  echo "==> E2E test suite FAILED (exit code $exit_code)" >&2
fi

exit $exit_code
