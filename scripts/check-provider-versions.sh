#!/usr/bin/env bash
set -euo pipefail

providers=(
  "claude:./node_modules/.bin/claude"
  "opencode:./node_modules/.bin/opencode"
  "codex:./node_modules/.bin/codex"
  "gemini:./node_modules/.bin/gemini"
)

echo "| Provider | Version |"
echo "|---|---|"
for entry in "${providers[@]}"; do
  name="${entry%%:*}"
  bin="${entry#*:}"
  if [[ ! -x "$bin" ]]; then
    echo "| $name | unavailable |"
    continue
  fi
  version="$(timeout 15s "$bin" --version 2>&1 || true)"
  version="$(printf '%s\n' "$version" | head -n 1 | tr '|' '/')"
  version="${version//$'\n'/ }"
  if [[ -z "$version" ]]; then
    version="unknown"
  fi
  echo "| $name | ${version:-unknown} |"
done
