#!/usr/bin/env bash
set -euo pipefail

export GOCACHE="${GOCACHE:-/tmp/go-build}"
export GOMODCACHE="${GOMODCACHE:-/tmp/go-mod}"
export GOLANGCI_LINT_CACHE="${GOLANGCI_LINT_CACHE:-/tmp/golangci-lint}"

golangci-lint run ./...
