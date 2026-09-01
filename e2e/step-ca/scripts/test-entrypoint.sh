#!/usr/bin/env bash
set -euo pipefail

# In Step CA mode, EnsurePKI generates the ca-bundle.crt, server cert, and
# local-client cert in the bridge's state dir (~/.config/bridgectl/certs/).
# The bridge-state Docker volume is shared at /bridge-state in this container.
BRIDGE_CERTS_DIR=/bridge-state/certs

echo "==> Waiting for bridge server PKI material..."
for i in $(seq 1 120); do
  if [ -f "$BRIDGE_CERTS_DIR/ca-bundle.crt" ] && \
     [ -f "$BRIDGE_CERTS_DIR/local-client.crt" ] && \
     [ -f "$BRIDGE_CERTS_DIR/local-client.key" ]; then
    echo "    PKI material found."
    break
  fi
  if [ "$i" -eq 120 ]; then
    echo "ERROR: timed out waiting for PKI material after 120s" >&2
    echo "  Files in $BRIDGE_CERTS_DIR:" >&2
    ls -la "$BRIDGE_CERTS_DIR/" 2>/dev/null || echo "  (directory does not exist)" >&2
    exit 1
  fi
  sleep 1
done

echo "==> Bridge PKI files:"
ls -la "$BRIDGE_CERTS_DIR/"

RESULTS_DIR="/results"
mkdir -p "$RESULTS_DIR"

echo "==> Running step-ca e2e test suite..."
gotestsum \
  --format short-verbose \
  --junitfile "$RESULTS_DIR/stepca-e2e.xml" \
  --raw-command -- stepca-e2e \
  -test.v \
  -test.timeout 180s \
  -bridge.target bridge:9445 \
  -bridge.cacert "$BRIDGE_CERTS_DIR/ca-bundle.crt" \
  -bridge.cert "$BRIDGE_CERTS_DIR/local-client.crt" \
  -bridge.key "$BRIDGE_CERTS_DIR/local-client.key" \
  -bridge.timeout 120s

exit_code=$?

if [ $exit_code -eq 0 ]; then
  echo "==> Step CA e2e test suite PASSED"
else
  echo "==> Step CA e2e test suite FAILED (exit code $exit_code)" >&2
fi

exit $exit_code
