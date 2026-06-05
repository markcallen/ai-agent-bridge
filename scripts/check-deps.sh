#!/usr/bin/env bash
set -euo pipefail

# Validates that all required development tools are installed and compatible.
# Usage: make check-deps

ERRORS=0

check() {
  local name="$1"
  local cmd="$2"
  if command -v "$cmd" >/dev/null 2>&1; then
    printf "  %-20s %s\n" "$name" "$(command -v "$cmd")"
  else
    printf "  %-20s MISSING — install %s\n" "$name" "$name"
    ERRORS=$((ERRORS + 1))
  fi
}

echo "=== Checking required tools ==="
echo ""

# Go toolchain
check "go" "go"
check "gofmt" "gofmt"
check "goimports" "goimports"
check "golangci-lint" "golangci-lint"
check "protoc" "protoc"

# Node / npm (for AI agent CLIs and TypeScript linting)
check "node" "node"
check "npm" "npm"

# Git hooks
check "pre-commit" "pre-commit"

echo ""

# Verify Go version compatibility between go and golangci-lint.
if command -v go >/dev/null 2>&1 && command -v golangci-lint >/dev/null 2>&1; then
  GO_MAJOR_MINOR="$(go version | grep -oE 'go[0-9]+\.[0-9]+' | head -1 | sed 's/go//')"
  LINT_GO="$(golangci-lint --version 2>&1 | grep -oE 'go[0-9]+\.[0-9]+' | head -1 | sed 's/go//')"

  if [ -n "$GO_MAJOR_MINOR" ] && [ -n "$LINT_GO" ]; then
    if [ "$GO_MAJOR_MINOR" != "$LINT_GO" ]; then
      echo "WARNING: go toolchain is $GO_MAJOR_MINOR but golangci-lint was built with go$LINT_GO"
      echo "  This can cause panics. Upgrade golangci-lint:"
      echo "    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"
      echo ""
      ERRORS=$((ERRORS + 1))
    else
      echo "Go/golangci-lint version match: go$GO_MAJOR_MINOR"
    fi
  fi
fi

# Verify eslint is available (npm install must have been run).
if command -v npm >/dev/null 2>&1; then
  if npm run lint:ts -- --help >/dev/null 2>&1; then
    echo "eslint: available via npm"
  else
    echo "WARNING: eslint not available — run 'npm install' at project root"
    ERRORS=$((ERRORS + 1))
  fi
fi

# Verify .nvmrc node version matches active node.
if [ -f .nvmrc ] && command -v node >/dev/null 2>&1; then
  NVMRC_MAJOR="$(tr -d '[:space:]' < .nvmrc)"
  NODE_MAJOR="$(node -p 'process.versions.node.split(".")[0]')"
  if [ "$NVMRC_MAJOR" != "$NODE_MAJOR" ]; then
    echo "WARNING: .nvmrc requires Node $NVMRC_MAJOR but active Node is $NODE_MAJOR"
    echo "  Run: nvm use"
    ERRORS=$((ERRORS + 1))
  else
    echo "Node version match: v$NODE_MAJOR (from .nvmrc)"
  fi
fi

echo ""
if [ "$ERRORS" -gt 0 ]; then
  echo "=== $ERRORS issue(s) found — fix before committing ==="
  exit 1
else
  echo "=== All dependencies OK ==="
fi
