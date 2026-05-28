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

GOARCH="$ARCH" go build -o "$ROOT_DIR/bin/bridge" ./cmd/bridge
GOARCH="$ARCH" go build -o "$ROOT_DIR/bin/bridge-ca" ./cmd/bridge-ca

mkdir -p \
  "$PKG_ROOT/DEBIAN" \
  "$PKG_ROOT/usr/bin" \
  "$PKG_ROOT/etc/ai-agent-bridge" \
  "$PKG_ROOT/lib/systemd/system"

install -m 0755 "$ROOT_DIR/bin/bridge" "$PKG_ROOT/usr/bin/bridge"
install -m 0755 "$ROOT_DIR/bin/bridge-ca" "$PKG_ROOT/usr/bin/bridge-ca"
install -m 0644 "$ROOT_DIR/packaging/bridge.yaml" "$PKG_ROOT/etc/ai-agent-bridge/bridge.yaml"
install -m 0644 "$ROOT_DIR/packaging/ai-agent-bridge.service" "$PKG_ROOT/lib/systemd/system/ai-agent-bridge.service"
install -m 0755 "$ROOT_DIR/packaging/debian/postinst" "$PKG_ROOT/DEBIAN/postinst"
install -m 0755 "$ROOT_DIR/packaging/debian/prerm" "$PKG_ROOT/DEBIAN/prerm"

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
