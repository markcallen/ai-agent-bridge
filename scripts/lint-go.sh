#!/usr/bin/env bash
set -euo pipefail

export GOLANGCI_LINT_CACHE="${GOLANGCI_LINT_CACHE:-/tmp/golangci-lint}"

golangci-lint run ./...
