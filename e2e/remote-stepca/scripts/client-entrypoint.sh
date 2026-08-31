#!/usr/bin/env bash
set -euo pipefail

CLIENT_STATE=/home/testuser/.config/bridgectl
STEP_CA_ROOT=/step-ca/shared/root_ca.crt

echo "==> Setting up client user..."
useradd -m -s /bin/bash testuser 2>/dev/null || true

echo "==> Waiting for Step CA root cert..."
for i in $(seq 1 60); do
  if [ -f "$STEP_CA_ROOT" ]; then
    echo "    Step CA root found."
    break
  fi
  if [ "$i" -eq 60 ]; then
    echo "ERROR: timed out waiting for Step CA root" >&2
    exit 1
  fi
  sleep 1
done

echo "==> Obtaining client certificate from Step CA..."
mkdir -p "$CLIENT_STATE/certs"
cp "$STEP_CA_ROOT" "$CLIENT_STATE/certs/step-ca-root.crt"

# Write provisioner password to a temp file for non-interactive cert request.
PW_FILE=$(mktemp)
echo -n "$STEP_CA_PROVISIONER_PASSWORD" > "$PW_FILE"

# Use bridgectl client init to get a cert from Step CA and enroll with server.
BRIDGECTL_STATE_DIR="$CLIENT_STATE" bridgectl client init \
  --step-ca-url "$STEP_CA_URL" \
  --step-ca-root "$STEP_CA_ROOT" \
  --provisioner admin \
  --step-ca-provisioner-password-file "$PW_FILE" \
  --name remote-stepca-client \
  --target "$BRIDGE_SERVER"

rm -f "$PW_FILE"

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

echo "==> Running remote Step CA e2e test suite..."
E2E_TEST_TIMEOUT="${E2E_TEST_TIMEOUT:-600s}"

remote-stepca-e2e \
  -test.v \
  -test.timeout "$E2E_TEST_TIMEOUT" \
  -server "$BRIDGE_SERVER" \
  -client-state "$CLIENT_STATE" \
  -repo /tmp/bridgectl

exit_code=$?

if [ $exit_code -eq 0 ]; then
  echo "==> Remote Step CA e2e test suite PASSED"
else
  echo "==> Remote Step CA e2e test suite FAILED (exit code $exit_code)" >&2
fi

exit $exit_code
