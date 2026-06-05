#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-}"
ARCH="${ARCH:-amd64}"
OUTPUT_DIR="${OUTPUT_DIR:-$ROOT_DIR/dist/deb}"
PACKAGE_NAME="ai-agent-bridge"
PKG_ROOT="$(mktemp -d)"
: "${GOCACHE:=/tmp/ai-agent-bridge-go-build}"
: "${GOFLAGS:=-buildvcs=false}"

export GOCACHE
export GOFLAGS

cleanup() {
  rm -rf "$PKG_ROOT"
}

trap cleanup EXIT

if [[ -z "$VERSION" ]]; then
  echo "build-deb: VERSION is required" >&2
  exit 1
fi

if ! command -v dpkg-deb >/dev/null 2>&1; then
  echo "build-deb: dpkg-deb is required" >&2
  exit 1
fi

mkdir -p "$OUTPUT_DIR"

mkdir -p "$ROOT_DIR/bin"
GOARCH="$ARCH" go build -o "$ROOT_DIR/bin/ai-agent-bridge" ./cmd/bridge
GOARCH="$ARCH" go build -o "$ROOT_DIR/bin/ai-agent-bridge-ca" ./cmd/bridge-ca
GOARCH="$ARCH" go build -o "$ROOT_DIR/bin/bridgectl" ./cmd/bridgectl

mkdir -p \
  "$PKG_ROOT/DEBIAN" \
  "$PKG_ROOT/usr/bin" \
  "$PKG_ROOT/usr/lib/ai-agent-bridge" \
  "$PKG_ROOT/usr/share/ai-agent-bridge/provider-runtime" \
  "$PKG_ROOT/usr/share/doc/ai-agent-bridge/examples" \
  "$PKG_ROOT/etc/ai-agent-bridge" \
  "$PKG_ROOT/lib/systemd/system"

# Binaries
install -m 0755 "$ROOT_DIR/bin/ai-agent-bridge"    "$PKG_ROOT/usr/bin/ai-agent-bridge"
install -m 0755 "$ROOT_DIR/bin/ai-agent-bridge-ca" "$PKG_ROOT/usr/bin/ai-agent-bridge-ca"
install -m 0755 "$ROOT_DIR/bin/bridgectl" "$PKG_ROOT/usr/bin/bridgectl"

# Default daemon config and systemd unit
install -m 0644 "$ROOT_DIR/packaging/bridge.yaml" \
  "$PKG_ROOT/etc/ai-agent-bridge/bridge.yaml"
install -m 0644 "$ROOT_DIR/packaging/ai-agent-bridge.service" \
  "$PKG_ROOT/lib/systemd/system/ai-agent-bridge.service"

# Provider runtime install helper and doctor script
install -m 0755 "$ROOT_DIR/packaging/install-provider-runtime" \
  "$PKG_ROOT/usr/lib/ai-agent-bridge/install-provider-runtime"
install -m 0755 "$ROOT_DIR/scripts/ai-desktops-doctor" \
  "$PKG_ROOT/usr/lib/ai-agent-bridge/ai-desktops-doctor"

# Provider runtime manifest (used by install-provider-runtime)
install -m 0644 "$ROOT_DIR/.nvmrc"            "$PKG_ROOT/usr/share/ai-agent-bridge/provider-runtime/.nvmrc"
install -m 0644 "$ROOT_DIR/package.json"      "$PKG_ROOT/usr/share/ai-agent-bridge/provider-runtime/package.json"
install -m 0644 "$ROOT_DIR/package-lock.json" "$PKG_ROOT/usr/share/ai-agent-bridge/provider-runtime/package-lock.json"

# Example configs and systemd drop-in
install -m 0644 "$ROOT_DIR/packaging/examples/bridge-ai-desktops.yaml" \
  "$PKG_ROOT/usr/share/doc/ai-agent-bridge/examples/bridge-ai-desktops.yaml"
install -m 0644 "$ROOT_DIR/packaging/systemd/ai-desktops.conf" \
  "$PKG_ROOT/usr/share/doc/ai-agent-bridge/examples/ai-desktops.conf"

# Debian maintainer scripts
install -m 0755 "$ROOT_DIR/packaging/debian/postinst" "$PKG_ROOT/DEBIAN/postinst"
install -m 0755 "$ROOT_DIR/packaging/debian/prerm"    "$PKG_ROOT/DEBIAN/prerm"

cat >"$PKG_ROOT/DEBIAN/control" <<EOF
Package: $PACKAGE_NAME
Version: $VERSION
Section: admin
Priority: optional
Architecture: $ARCH
Maintainer: Mark Callen <opensource@markcallen.com>
Depends: adduser, ca-certificates
Description: AI Agent Bridge daemon
 Standalone gRPC daemon and SDK bridge for supervising AI agent subprocesses.
 This package installs the bridge binaries, a default config, and a systemd unit.
EOF

dpkg-deb --build --root-owner-group "$PKG_ROOT" "$OUTPUT_DIR/${PACKAGE_NAME}_${VERSION}_${ARCH}.deb"
