#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DESTROY_MODE="always"
ACTION="run"

usage() {
  cat <<EOF
Usage: $(basename "$0") [run|status] [--keep-alive]

Environment:
  SMOKE_AWS_REGION     Required AWS region
  SMOKE_APT_SUITE      Optional Ubuntu suite for install smoke (default: noble)
  SMOKE_REPO_BASE_URL  Optional apt repository URL override
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    run|status)
      ACTION="$1"
      shift
      ;;
    --keep-alive)
      DESTROY_MODE="manual"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "smoke-ec2: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ "$ACTION" == "status" ]]; then
  exec "$ROOT_DIR/scripts/ec2-smoke-test.sh" status
fi

cleanup() {
  local rc=$?
  if [[ "$DESTROY_MODE" == "always" ]]; then
    "$ROOT_DIR/scripts/ec2-smoke-test.sh" destroy >/dev/null 2>&1 || true
  fi
  exit "$rc"
}

trap cleanup EXIT

"$ROOT_DIR/scripts/ec2-smoke-test.sh" run
