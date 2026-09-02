#!/usr/bin/env bash
set -euo pipefail

CLIENT_STATE=/home/testuser/.config/bridgectl
RESULTS_DIR=/results
RESULTS_FILE="$RESULTS_DIR/test-results.txt"

mkdir -p "$RESULTS_DIR"

# Write results on exit.
cleanup() {
  local rc=$?
  echo "" >> "$RESULTS_FILE"
  if [ $rc -eq 0 ]; then
    echo "=== RESULT: PASS ===" | tee -a "$RESULTS_FILE"
  else
    echo "=== RESULT: FAIL (exit code $rc) ===" | tee -a "$RESULTS_FILE"
  fi
  echo "Results written to $RESULTS_FILE"
  cat "$RESULTS_FILE"
}
trap cleanup EXIT

echo "=== Remote mTLS E2E Test ===" | tee "$RESULTS_FILE"
echo "Started: $(date -u +%Y-%m-%dT%H:%M:%SZ)" | tee -a "$RESULTS_FILE"
echo "" | tee -a "$RESULTS_FILE"

echo "==> Setting up client user..."
useradd -m -s /bin/bash testuser 2>/dev/null || true

echo "==> Waiting for client credential bundle..."
BUNDLE_PATH=""
for i in $(seq 1 90); do
  for f in /client-creds/*-creds.tar.gz; do
    if [ -f "$f" ]; then
      BUNDLE_PATH="$f"
      break 2
    fi
  done
  if [ "$i" -eq 90 ]; then
    echo "ERROR: timed out waiting for credential bundle in /client-creds/" | tee -a "$RESULTS_FILE"
    ls -la /client-creds/ 2>/dev/null || true
    exit 1
  fi
  sleep 1
done
echo "    Found bundle: $BUNDLE_PATH" | tee -a "$RESULTS_FILE"

echo "==> Step: bridgectl client setup --bundle" | tee -a "$RESULTS_FILE"
mkdir -p "$CLIENT_STATE"
BRIDGECTL_STATE_DIR="$CLIENT_STATE" bridgectl client setup --bundle "$BUNDLE_PATH" 2>&1 | tee -a "$RESULTS_FILE"

echo "" | tee -a "$RESULTS_FILE"
echo "==> Client credentials:" | tee -a "$RESULTS_FILE"
ls -la "$CLIENT_STATE/certs/" 2>&1 | tee -a "$RESULTS_FILE"

echo "" | tee -a "$RESULTS_FILE"
echo "==> Step: bridgectl client enroll (auto-discovery)" | tee -a "$RESULTS_FILE"
BRIDGECTL_STATE_DIR="$CLIENT_STATE" bridgectl client enroll \
  --target "$BRIDGE_SERVER" \
  --server-name bridge-server 2>&1 | tee -a "$RESULTS_FILE"

echo "" | tee -a "$RESULTS_FILE"
echo "==> Step: bridgectl identity show" | tee -a "$RESULTS_FILE"
BRIDGECTL_STATE_DIR="$CLIENT_STATE" bridgectl identity show 2>&1 | tee -a "$RESULTS_FILE"

echo "" | tee -a "$RESULTS_FILE"
echo "==> Preparing test repository..."
if [ -d "/tmp/bridgectl/.git" ]; then
  git -C /tmp/bridgectl pull origin main 2>/dev/null || true
else
  git clone --depth 1 https://github.com/orchael/bridge-demo /tmp/bridgectl-src
  cp -a /tmp/bridgectl-src/. /tmp/bridgectl/
  rm -rf /tmp/bridgectl-src
fi
chmod -R a+rwX /tmp/bridgectl

echo "" | tee -a "$RESULTS_FILE"
echo "==> Running Go e2e test suite..." | tee -a "$RESULTS_FILE"
E2E_TEST_TIMEOUT="${E2E_TEST_TIMEOUT:-600s}"

gotestsum \
  --format short-verbose \
  --junitfile "$RESULTS_DIR/remote-mtls-e2e.xml" \
  --raw-command -- test2json -t -p remote-mtls remote-mtls-e2e \
  -test.v \
  -test.timeout "$E2E_TEST_TIMEOUT" \
  -server "$BRIDGE_SERVER" \
  -client-state "$CLIENT_STATE" \
  -repo /tmp/bridgectl 2>&1 | tee -a "$RESULTS_FILE"
