#!/usr/bin/env bash
set -euo pipefail

echo "==> Running Go tests"
go test -race -count=1 ./...

echo "==> Enforcing maintained package coverage"
./scripts/check-go-coverage.sh

echo "==> Building TypeScript packages"
corepack pnpm --dir examples/web/ui install --frozen-lockfile
corepack pnpm --dir examples/web/ui run build

echo "==> Building documentation site"
corepack pnpm --dir docs install --frozen-lockfile
corepack pnpm --dir docs run build
