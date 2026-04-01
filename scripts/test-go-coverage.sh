#!/usr/bin/env bash
set -euo pipefail

export GOCACHE="${GOCACHE:-/tmp/go-build}"
export GOMODCACHE="${GOMODCACHE:-/tmp/go-mod}"

mapfile -t packages < <(go list ./... | grep -v '/node_modules/')

if [ "${#packages[@]}" -eq 0 ]; then
  echo "no Go packages found"
  exit 1
fi

go test -race -covermode=atomic -coverprofile=coverage.out "${packages[@]}"
