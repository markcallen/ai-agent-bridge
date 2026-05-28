#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_DIR="${REPO_DIR:-$ROOT_DIR/dist/apt-repo}"
PACKAGES_DIR="${PACKAGES_DIR:-$ROOT_DIR/dist/deb}"
ARCHES="${ARCHES:-amd64}"
SUITES="${SUITES:-noble plucky}"
COMPONENT="${COMPONENT:-main}"
KEYRING_NAME="${KEYRING_NAME:-ai-agent-bridge-archive-keyring.asc}"
gpg_cmd=(gpg --batch --yes)

if ! command -v dpkg-scanpackages >/dev/null 2>&1; then
  echo "build-apt-repo: dpkg-scanpackages is required" >&2
  exit 1
fi

if ! command -v apt-ftparchive >/dev/null 2>&1; then
  echo "build-apt-repo: apt-ftparchive is required" >&2
  exit 1
fi

if ! command -v gpg >/dev/null 2>&1; then
  echo "build-apt-repo: gpg is required" >&2
  exit 1
fi

if [[ -n "${GPG_PASSPHRASE:-}" ]]; then
  gpg_cmd+=(--pinentry-mode loopback --passphrase "$GPG_PASSPHRASE")
fi

shopt -s nullglob
packages=("$PACKAGES_DIR"/*.deb)
shopt -u nullglob

if [[ ${#packages[@]} -eq 0 ]]; then
  echo "build-apt-repo: no .deb packages found in $PACKAGES_DIR" >&2
  exit 1
fi

mkdir -p "$REPO_DIR/pool/$COMPONENT"
cp "${packages[@]}" "$REPO_DIR/pool/$COMPONENT/"

"${gpg_cmd[@]}" --armor --export >"$REPO_DIR/$KEYRING_NAME"

pushd "$REPO_DIR" >/dev/null

for suite in $SUITES; do
  for arch in $ARCHES; do
    binary_dir="dists/$suite/$COMPONENT/binary-$arch"
    mkdir -p "$binary_dir"
    dpkg-scanpackages --arch "$arch" "pool/$COMPONENT" >"$binary_dir/Packages"
    gzip -9c "$binary_dir/Packages" >"$binary_dir/Packages.gz"
  done

  cat >"dists/$suite/Release" <<EOF
Origin: AI Agent Bridge
Label: AI Agent Bridge
Suite: $suite
Codename: $suite
Architectures: $ARCHES
Components: $COMPONENT
Description: Apt repository for ai-agent-bridge
EOF
  apt-ftparchive release "dists/$suite" >>"dists/$suite/Release"

  "${gpg_cmd[@]}" --armor --detach-sign \
    --output "dists/$suite/Release.gpg" \
    "dists/$suite/Release"
  "${gpg_cmd[@]}" --clearsign \
    --output "dists/$suite/InRelease" \
    "dists/$suite/Release"
done

popd >/dev/null
