#!/usr/bin/env bash
set -euo pipefail

SERVER_STATE=/server-state
CLIENT_STATE=/home/testuser/.config/bridgectl

echo "==> Setting up client user..."
useradd -m -s /bin/bash testuser 2>/dev/null || true

echo "==> Waiting for server PKI material..."
for i in $(seq 1 60); do
  if [ -f "$SERVER_STATE/certs/ca-bundle.crt" ]; then
    echo "    Server PKI found."
    break
  fi
  if [ "$i" -eq 60 ]; then
    echo "ERROR: timed out waiting for server PKI" >&2
    exit 1
  fi
  sleep 1
done

echo "==> Issuing client credentials on server..."
# Use the server's state to issue a client cert (simulates server admin action).
BRIDGECTL_STATE_DIR="$SERVER_STATE" bridgectl server issue-client \
  --name remote-client \
  --bundle

BUNDLE_PATH="$SERVER_STATE/certs/clients/remote-client/remote-client-creds.tar.gz"
if [ ! -f "$BUNDLE_PATH" ]; then
  echo "ERROR: bundle not created at $BUNDLE_PATH" >&2
  ls -la "$SERVER_STATE/certs/clients/remote-client/" 2>/dev/null || true
  exit 1
fi

echo "==> Running 'bridgectl client setup --bundle' (v1.1 deployment flow)..."
mkdir -p "$CLIENT_STATE"
BRIDGECTL_STATE_DIR="$CLIENT_STATE" bridgectl client setup --bundle "$BUNDLE_PATH"

echo "==> Client credentials directory:"
ls -la "$CLIENT_STATE/certs/"

echo "==> Enrolling with server (auto-discovery)..."
BRIDGECTL_STATE_DIR="$CLIENT_STATE" bridgectl client enroll \
  --target "$BRIDGE_SERVER" \
  --server-name bridge-server

echo "==> Verifying identity..."
BRIDGECTL_STATE_DIR="$CLIENT_STATE" bridgectl identity show

echo "==> Preparing test repository..."
if [ -d "/tmp/bridgectl/.git" ]; then
  git -C /tmp/bridgectl pull origin main 2>/dev/null || true
else
  git clone --depth 1 https://github.com/orchael/bridge-demo /tmp/bridgectl-src
  cp -a /tmp/bridgectl-src/. /tmp/bridgectl/
  rm -rf /tmp/bridgectl-src
fi
chmod -R a+rwX /tmp/bridgectl

echo "==> Running remote mTLS e2e test suite..."
E2E_TEST_TIMEOUT="${E2E_TEST_TIMEOUT:-600s}"

remote-mtls-e2e \
  -test.v \
  -test.timeout "$E2E_TEST_TIMEOUT" \
  -server "$BRIDGE_SERVER" \
  -client-state "$CLIENT_STATE" \
  -repo /tmp/bridgectl

exit_code=$?

if [ $exit_code -eq 0 ]; then
  echo "==> Remote mTLS e2e test suite PASSED"
else
  echo "==> Remote mTLS e2e test suite FAILED (exit code $exit_code)" >&2
fi

exit $exit_code
