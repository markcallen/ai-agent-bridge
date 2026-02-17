#!/usr/bin/env bash
set -euo pipefail

CERT_DIR=/app/certs

# Running as root — fix volume ownership so the bridge user can read certs.
chown bridge:bridge "$CERT_DIR"

echo "==> Initializing CA..."
bridge-ca init --name bridge-e2e-ca --out "$CERT_DIR"

echo "==> Issuing server certificate..."
bridge-ca issue --type server --cn bridge \
  --san bridge,localhost,127.0.0.1 \
  --ca "$CERT_DIR/ca.crt" --ca-key "$CERT_DIR/ca.key" \
  --out "$CERT_DIR"

echo "==> Issuing client certificate..."
bridge-ca issue --type client --cn e2e-client \
  --ca "$CERT_DIR/ca.crt" --ca-key "$CERT_DIR/ca.key" \
  --out "$CERT_DIR"

echo "==> Generating JWT signing keypair..."
bridge-ca jwt-keygen --out "$CERT_DIR/jwt-signing"

echo "==> Building trust bundle..."
bridge-ca bundle --out "$CERT_DIR/ca-bundle.crt" "$CERT_DIR/ca.crt"

# Make certs readable by bridge user and test-client
chmod 644 "$CERT_DIR"/*

echo "==> Starting bridge as non-root user..."
exec su -s /bin/bash bridge -c "exec bridge --config /app/config/bridge-e2e.yaml"
