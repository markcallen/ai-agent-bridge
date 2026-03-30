#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_DIR"

if [ ! -f package.json ]; then
  echo "package.json not found in $PROJECT_DIR"
  exit 1
fi

echo "==> Installing pinned AI agent CLIs into $PROJECT_DIR/node_modules"
npm install

echo "==> Verifying installed CLI versions"
./node_modules/.bin/claude --version
./node_modules/.bin/codex --version || true
./node_modules/.bin/gemini --version || true
./node_modules/.bin/opencode --version || true

echo
echo "==> Installed local agent binaries:"
echo "  Claude:   $PROJECT_DIR/node_modules/.bin/claude"
echo "  Codex:    $PROJECT_DIR/node_modules/.bin/codex"
echo "  Gemini:   $PROJECT_DIR/node_modules/.bin/gemini"
echo "  OpenCode: $PROJECT_DIR/node_modules/.bin/opencode"
