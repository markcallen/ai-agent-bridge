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
if [ -d "/tmp/cache-cleaner/.git" ]; then
  echo "    Repo already present, pulling latest main..."
  git -C /tmp/cache-cleaner pull origin main
else
  git clone --depth 1 https://github.com/markcallen/cache-cleaner /tmp/cache-cleaner-src
  cp -a /tmp/cache-cleaner-src/. /tmp/cache-cleaner/
  rm -rf /tmp/cache-cleaner-src
fi

echo "==> Running e2e test..."
e2e-test \
  -target bridge:9445 \
  -cacert "$CERT_DIR/ca-bundle.crt" \
  -cert "$CERT_DIR/e2e-client.crt" \
  -key "$CERT_DIR/e2e-client.key" \
  -jwt-key "$CERT_DIR/jwt-signing.key" \
  -jwt-issuer e2e \
  -repo /tmp/cache-cleaner \
  -timeout 120s

exit_code=$?

if [ $exit_code -eq 0 ]; then
  echo "==> E2E test PASSED"
else
  echo "==> E2E test FAILED (exit code $exit_code)" >&2
fi

exit $exit_code
