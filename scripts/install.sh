#!/usr/bin/env bash
set -euo pipefail

if [[ $(id -u) -ne 0 ]]; then
  exec sudo -E bash "$0" "$@"
fi

REPO_BASE_URL="${REPO_BASE_URL:-https://markcallen.github.io/ai-agent-bridge/apt}"
KEYRING_PATH="/etc/apt/keyrings/ai-agent-bridge.gpg"
LIST_PATH="/etc/apt/sources.list.d/ai-agent-bridge.list"

suite="${APT_SUITE:-}"
if [[ -z "$suite" && -r /etc/os-release ]]; then
  # shellcheck disable=SC1091
  . /etc/os-release
  suite="${VERSION_CODENAME:-}"
fi

case "$suite" in
  noble|resolute)
    ;;
  *)
    echo "install.sh: unsupported Ubuntu suite: ${suite:-unknown}" >&2
    echo "Supported suites: noble, resolute" >&2
    exit 1
    ;;
esac

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ca-certificates curl gnupg
install -d -m 0755 /etc/apt/keyrings
curl -fsSL "$REPO_BASE_URL/ai-agent-bridge-archive-keyring.asc" | gpg --dearmor -o "$KEYRING_PATH"
chmod 0644 "$KEYRING_PATH"
cat >"$LIST_PATH" <<EOF
deb [arch=amd64 signed-by=$KEYRING_PATH] $REPO_BASE_URL $suite main
EOF
apt-get update
apt-get install -y ai-agent-bridge
