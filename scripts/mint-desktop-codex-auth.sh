#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Usage: mint-desktop-codex-auth.sh [options]

Seeds and refreshes a desktop-specific Codex auth.json using Codex's normal
refresh flow. This does not call OAuth token endpoints directly.

Options:
  --codex-home DIR       Persistent CODEX_HOME for this desktop.
                         Default: ~/.codex-desktops/<hostname>
  --agents-env FILE      agents.env file to update with CODEX_HOME and without
                         CODEX_AUTH. Default: ~/.config/bridgectl/agents.env
  --auth-json FILE       Seed auth.json from this file if CODEX_HOME/auth.json
                         does not already exist.
  --no-agents-env        Do not update agents.env.
  --skip-refresh         Only seed/validate auth.json; do not run Codex.
  -h, --help             Show this help.

Seed sources, in order, when CODEX_HOME/auth.json is missing:
  1. --auth-json FILE
  2. CODEX_AUTH_JSON environment variable
  3. CODEX_AUTH environment variable

After seeding, the script runs:
  codex exec --json "Reply with the single word OK."

That normal Codex run is what refreshes auth.json when Codex considers the
session stale. Persist CODEX_HOME per desktop; do not share it across machines.
USAGE
}

short_hostname() {
  hostname -s 2>/dev/null || hostname
}

codex_home="${CODEX_HOME:-$HOME/.codex-desktops/$(short_hostname)}"
agents_env="${BRIDGECTL_AGENTS_ENV:-$HOME/.config/bridgectl/agents.env}"
auth_json=""
update_agents_env=1
refresh=1

while [ "$#" -gt 0 ]; do
  case "$1" in
    --codex-home)
      codex_home="${2:?--codex-home requires a directory}"
      shift 2
      ;;
    --agents-env)
      agents_env="${2:?--agents-env requires a file}"
      shift 2
      ;;
    --auth-json)
      auth_json="${2:?--auth-json requires a file}"
      shift 2
      ;;
    --no-agents-env)
      update_agents_env=0
      shift
      ;;
    --skip-refresh)
      refresh=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "mint-desktop-codex-auth: unknown argument: $1" >&2
      usage
      exit 2
      ;;
  esac
done

auth_file="$codex_home/auth.json"
if [ "$update_agents_env" -eq 1 ] && [[ "$codex_home" =~ [[:space:]] ]]; then
  cat >&2 <<EOF
mint-desktop-codex-auth: CODEX_HOME contains whitespace and cannot be written
as an unquoted agents.env entry: $codex_home

Use a path without whitespace, or pass --no-agents-env and manage CODEX_HOME
quoting in your service environment.
EOF
  exit 1
fi
mkdir -p "$codex_home"
chmod 700 "$codex_home"

if [ ! -f "$auth_file" ]; then
  if [ -n "$auth_json" ]; then
    install -m 0600 "$auth_json" "$auth_file"
  elif [ -n "${CODEX_AUTH_JSON:-}" ]; then
    umask 077
    printf '%s' "$CODEX_AUTH_JSON" > "$auth_file"
  elif [ -n "${CODEX_AUTH:-}" ]; then
    umask 077
    printf '%s' "$CODEX_AUTH" > "$auth_file"
  else
    cat >&2 <<EOF
mint-desktop-codex-auth: $auth_file does not exist and no seed was provided.

Run 'codex login' on a trusted machine with file-backed credential storage,
then pass that auth.json with --auth-json, or provide CODEX_AUTH_JSON.
EOF
    exit 1
  fi
fi
chmod 600 "$auth_file"

if command -v jq >/dev/null 2>&1; then
  auth_mode="$(jq -r '.auth_mode // ""' "$auth_file")"
  has_refresh_token="$(jq -r '((.tokens.refresh_token // "") != "")' "$auth_file")"
  if [ "$auth_mode" != "chatgpt" ]; then
    echo "mint-desktop-codex-auth: auth.json auth_mode is $auth_mode, expected chatgpt" >&2
    exit 1
  fi
  if [ "$has_refresh_token" != "true" ]; then
    echo "mint-desktop-codex-auth: auth.json does not contain a refresh token" >&2
    exit 1
  fi
else
  echo "mint-desktop-codex-auth: jq not found; skipping auth.json shape validation" >&2
fi

if [ "$refresh" -eq 1 ]; then
  if ! command -v codex >/dev/null 2>&1; then
    echo "mint-desktop-codex-auth: codex not found on PATH" >&2
    exit 1
  fi
  CODEX_HOME="$codex_home" codex exec --json "Reply with the single word OK." >/dev/null
fi

if [ "$update_agents_env" -eq 1 ]; then
  mkdir -p "$(dirname "$agents_env")"
  tmp="$(mktemp "${agents_env}.XXXXXX")"
  if [ -f "$agents_env" ]; then
    grep -v -E '^(CODEX_AUTH|CODEX_HOME)=' "$agents_env" > "$tmp" || true
  fi
  printf 'CODEX_HOME=%s\n' "$codex_home" >> "$tmp"
  chmod 600 "$tmp"
  mv "$tmp" "$agents_env"
fi

cat <<EOF
Codex auth is ready for this desktop.
CODEX_HOME=$codex_home
auth.json=$auth_file
EOF
