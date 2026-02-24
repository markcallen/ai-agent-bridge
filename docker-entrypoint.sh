#!/usr/bin/env bash
set -euo pipefail

CERT_DIR="${CERT_DIR:-/app/certs}"
BRIDGE_CONFIG="${BRIDGE_CONFIG:-/app/config/bridge.yaml}"
BRIDGE_CN="${BRIDGE_CN:-bridge}"
BRIDGE_SANS="${BRIDGE_SANS:-bridge,localhost,127.0.0.1}"

# Running as root — fix volume ownership so the bridge user can read/write certs.
chown bridge:bridge "$CERT_DIR"

if [ ! -f "$CERT_DIR/ca.crt" ]; then
  echo "==> Initializing CA..."
  bridge-ca init --name bridge-ca --out "$CERT_DIR"

  echo "==> Issuing server certificate..."
  bridge-ca issue --type server --cn "$BRIDGE_CN" \
    --san "$BRIDGE_SANS" \
    --ca "$CERT_DIR/ca.crt" --ca-key "$CERT_DIR/ca.key" \
    --out "$CERT_DIR"

  echo "==> Issuing client certificate..."
  bridge-ca issue --type client --cn client \
    --ca "$CERT_DIR/ca.crt" --ca-key "$CERT_DIR/ca.key" \
    --out "$CERT_DIR"

  echo "==> Generating JWT signing keypair..."
  bridge-ca jwt-keygen --out "$CERT_DIR/jwt-signing"

  echo "==> Building trust bundle..."
  bridge-ca bundle --out "$CERT_DIR/ca-bundle.crt" "$CERT_DIR/ca.crt"

  chmod 644 "$CERT_DIR"/*
fi

echo "==> Starting bridge as non-root user..."
exec su -s /bin/bash bridge -c "exec bridge --config $BRIDGE_CONFIG"
