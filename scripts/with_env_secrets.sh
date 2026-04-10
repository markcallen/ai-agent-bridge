#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -eq 0 ]; then
  echo "usage: $0 <command> [args...]"
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
DOTENV_PATH="${ENV_SECRETS_DOTENV_PATH:-$PROJECT_DIR/.env}"
NVMRC_PATH="${PROJECT_DIR}/.nvmrc"

if [ -f "$DOTENV_PATH" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$DOTENV_PATH"
  set +a
fi

if [ -f "$NVMRC_PATH" ]; then
  required_node_major="$(tr -d '[:space:]' < "$NVMRC_PATH")"
  export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
  if [ -s "$NVM_DIR/nvm.sh" ]; then
    # shellcheck disable=SC1090
    . "$NVM_DIR/nvm.sh"
    if command -v nvm >/dev/null 2>&1; then
      nvm use "$required_node_major" >/dev/null
      if [ -n "${NVM_BIN:-}" ]; then
        export PATH="$NVM_BIN:$PATH"
      fi
      hash -r
    fi
  fi
fi

if [ -n "${ENV_SECRETS_AWS_SECRET:-}" ]; then
  if ! command -v env-secrets >/dev/null 2>&1; then
    echo "env-secrets is required when ENV_SECRETS_AWS_SECRET is set"
    exit 1
  fi

  args=(aws --secret "$ENV_SECRETS_AWS_SECRET")
  if [ -n "${ENV_SECRETS_AWS_PROFILE:-}" ]; then
    args+=(--profile "$ENV_SECRETS_AWS_PROFILE")
  fi
  if [ -n "${ENV_SECRETS_AWS_REGION:-}" ]; then
    args+=(--region "$ENV_SECRETS_AWS_REGION")
  fi

  exec env-secrets "${args[@]}" -- "$@"
fi

exec "$@"
