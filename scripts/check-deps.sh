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
check "protoc-gen-go" "protoc-gen-go"
check "protoc-gen-go-grpc" "protoc-gen-go-grpc"

# Protobuf well-known types (needed by make proto)
if command -v brew >/dev/null 2>&1; then
  PROTOC_INCLUDE="$(brew --prefix)/include"
  if [ -f "$PROTOC_INCLUDE/google/protobuf/timestamp.proto" ]; then
    printf "  %-20s %s\n" "proto includes" "$PROTOC_INCLUDE"
  else
    printf "  %-20s MISSING — install protobuf (brew install protobuf)\n" "proto includes"
    ERRORS=$((ERRORS + 1))
  fi
else
  printf "  %-20s SKIPPED — brew not found\n" "proto includes"
fi

# Node / pnpm (for AI agent CLIs and TypeScript linting)
check "node" "node"
check "pnpm" "pnpm"

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

# Verify eslint is available (pnpm install must have been run).
if command -v pnpm >/dev/null 2>&1; then
  if pnpm run lint:ts -- --help >/dev/null 2>&1; then
    echo "eslint: available via pnpm"
  else
    echo "WARNING: eslint not available — run 'pnpm install' at project root"
    ERRORS=$((ERRORS + 1))
  fi
fi

# Cross-platform Node.js detection and version check.
# Supports nvm, Homebrew, system packages, winget, and direct installs.
if [ -f .nvmrc ]; then
  NVMRC_MAJOR="$(tr -d '[:space:]' < .nvmrc)"
  if command -v node >/dev/null 2>&1; then
    NODE_MAJOR="$(node -p 'process.versions.node.split(".")[0]')"
    if [ "$NVMRC_MAJOR" != "$NODE_MAJOR" ]; then
      echo "WARNING: .nvmrc requires Node $NVMRC_MAJOR but active Node is $NODE_MAJOR"
      # Detect the OS and suggest platform-appropriate remediation.
      case "$(uname -s)" in
        Darwin)
          echo "  Upgrade Node.js using one of:"
          echo "    nvm install $NVMRC_MAJOR && nvm use    (if using nvm)"
          echo "    brew install node@$NVMRC_MAJOR         (if using Homebrew)"
          echo "    https://nodejs.org/en/download/         (direct download)"
          ;;
        Linux)
          echo "  Upgrade Node.js using one of:"
          echo "    nvm install $NVMRC_MAJOR && nvm use    (if using nvm)"
          echo "    curl -fsSL https://deb.nodesource.com/setup_${NVMRC_MAJOR}.x | sudo -E bash -"
          echo "    sudo apt-get install -y nodejs          (Ubuntu/Debian via NodeSource)"
          echo "    https://nodejs.org/en/download/         (direct download)"
          ;;
        MINGW*|MSYS*|CYGWIN*)
          echo "  Upgrade Node.js using one of:"
          echo "    nvm install $NVMRC_MAJOR               (if using nvm-windows)"
          echo "    winget install OpenJS.NodeJS.LTS        (winget)"
          echo "    choco install nodejs-lts                (Chocolatey)"
          echo "    https://nodejs.org/en/download/         (direct download)"
          ;;
        *)
          echo "  Run: nvm install $NVMRC_MAJOR && nvm use"
          echo "  Or download from: https://nodejs.org/en/download/"
          ;;
      esac
      ERRORS=$((ERRORS + 1))
    else
      echo "Node version match: v$NODE_MAJOR (from .nvmrc)"
    fi
  else
    echo "WARNING: Node.js is not installed (required: v$NVMRC_MAJOR from .nvmrc)"
    case "$(uname -s)" in
      Darwin)
        echo "  Install Node.js using one of:"
        echo "    nvm install                             (recommended, https://github.com/nvm-sh/nvm)"
        echo "    brew install node@$NVMRC_MAJOR          (Homebrew)"
        echo "    https://nodejs.org/en/download/          (direct download)"
        ;;
      Linux)
        echo "  Install Node.js using one of:"
        echo "    nvm install                             (recommended, https://github.com/nvm-sh/nvm)"
        echo "    curl -fsSL https://deb.nodesource.com/setup_${NVMRC_MAJOR}.x | sudo -E bash -"
        echo "    sudo apt-get install -y nodejs           (Ubuntu/Debian via NodeSource)"
        echo "    https://nodejs.org/en/download/          (direct download)"
        ;;
      MINGW*|MSYS*|CYGWIN*)
        echo "  Install Node.js using one of:"
        echo "    winget install OpenJS.NodeJS.LTS         (winget)"
        echo "    choco install nodejs-lts                 (Chocolatey)"
        echo "    https://nodejs.org/en/download/          (direct download)"
        ;;
      *)
        echo "  Install Node.js from: https://nodejs.org/en/download/"
        ;;
    esac
    echo ""
    echo "  For detailed setup guidance, run: scripts/setup-node.sh"
    ERRORS=$((ERRORS + 1))
  fi
fi

echo ""
if [ "$ERRORS" -gt 0 ]; then
  echo "=== $ERRORS issue(s) found — fix before committing ==="
  exit 1
else
  echo "=== All dependencies OK ==="
fi
