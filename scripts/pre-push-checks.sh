#!/usr/bin/env bash
set -euo pipefail

echo "==> Running Go tests"
go test -race -count=1 ./...

echo "==> Enforcing maintained package coverage"
./scripts/check-go-coverage.sh

echo "==> Building TypeScript packages"
npm --prefix packages/bridge-client-node run build
npm --prefix examples/chat-ts run build
corepack pnpm --dir examples/chat-web run build
