#!/usr/bin/env bash
set -euo pipefail

CERT_DIR="${CERT_DIR:-/app/certs}"
RUNTIME_CERT_DIR="${RUNTIME_CERT_DIR:-/run/bridge-certs}"
BRIDGE_CONFIG="${BRIDGE_CONFIG:-/app/config/bridge.yaml}"
BRIDGE_CN="${BRIDGE_CN:-bridge}"
BRIDGE_SANS="${BRIDGE_SANS:-bridge,localhost,127.0.0.1}"
BRIDGE_CLIENT_CN="${BRIDGE_CLIENT_CN:-client}"

# Running as root — prepare directories the bridge user needs.
mkdir -p "$CERT_DIR" "$RUNTIME_CERT_DIR"
chown bridge:bridge "$CERT_DIR"
mkdir -p /home/bridge/.gemini /home/bridge/.config
chown -R bridge:bridge /home/bridge

if [ ! -f "$CERT_DIR/ca.crt" ]; then
  echo "==> Initializing CA..."
  bridge-ca init --name bridge-ca --out "$CERT_DIR"

  echo "==> Issuing server certificate..."
  bridge-ca issue --type server --cn "$BRIDGE_CN" \
    --san "$BRIDGE_SANS" \
    --ca "$CERT_DIR/ca.crt" --ca-key "$CERT_DIR/ca.key" \
    --out "$CERT_DIR"

  echo "==> Issuing client certificate..."
  bridge-ca issue --type client --cn "$BRIDGE_CLIENT_CN" \
    --ca "$CERT_DIR/ca.crt" --ca-key "$CERT_DIR/ca.key" \
    --out "$CERT_DIR"

  echo "==> Generating JWT signing keypair..."
  bridge-ca jwt-keygen --out "$CERT_DIR/jwt-signing"

  echo "==> Building trust bundle..."
  bridge-ca bundle --out "$CERT_DIR/ca-bundle.crt" "$CERT_DIR/ca.crt"

  chmod 644 "$CERT_DIR"/*
fi

cp -f "$CERT_DIR"/* "$RUNTIME_CERT_DIR"/
chown -R bridge:bridge "$RUNTIME_CERT_DIR"
chmod 644 "$RUNTIME_CERT_DIR"/*.crt "$RUNTIME_CERT_DIR"/*.pub
chmod 600 "$RUNTIME_CERT_DIR"/*.key

if [ -n "${ANTHROPIC_API_KEY:-}" ]; then
  echo "==> Verifying Claude API-key auth..."
  CLAUDE_AUTH_STATUS="$(su -m -s /bin/bash bridge -c 'cd /app && export HOME=/home/bridge && ./node_modules/.bin/claude auth status' || true)"
  if [[ "$CLAUDE_AUTH_STATUS" != *'"loggedIn": true'* ]] || [[ "$CLAUDE_AUTH_STATUS" != *'"apiKeySource": "ANTHROPIC_API_KEY"'* ]]; then
    echo "Claude auth verification failed"
    echo "$CLAUDE_AUTH_STATUS"
    exit 1
  fi
fi

echo "==> Seeding Claude onboarding state..."
su -m -s /bin/bash bridge -c 'cd /app && export HOME=/home/bridge && node <<'\''EOF'\''
const fs = require("fs");
const path = require("path");

const statePath = path.join(process.env.HOME, ".claude.json");
const pkg = require("./node_modules/@anthropic-ai/claude-code/package.json");

let state = {};
try {
  state = JSON.parse(fs.readFileSync(statePath, "utf8"));
} catch (_) {
  state = {};
}

state.theme = state.theme || "dark";
state.hasCompletedOnboarding = true;
state.lastOnboardingVersion = pkg.version;

fs.writeFileSync(statePath, JSON.stringify(state, null, 2) + "\n");
EOF'

echo "==> Seeding Gemini onboarding state..."
su -m -s /bin/bash bridge -c 'cd /app && export HOME=/home/bridge && node <<'\''EOF'\''
const fs = require("fs");
const path = require("path");

const geminiDir = path.join(process.env.HOME, ".gemini");
const settingsPath = path.join(geminiDir, "settings.json");
const trustedFoldersPath = path.join(geminiDir, "trustedFolders.json");
const projectsPath = path.join(geminiDir, "projects.json");
const statePath = path.join(geminiDir, "state.json");

fs.mkdirSync(geminiDir, { recursive: true });

let settings = {};
try {
  settings = JSON.parse(fs.readFileSync(settingsPath, "utf8"));
} catch (_) {
  settings = {};
}

settings.security = settings.security || {};
settings.security.auth = settings.security.auth || {};
settings.security.auth.selectedType = "gemini-api-key";

fs.writeFileSync(settingsPath, JSON.stringify(settings, null, 2) + "\n");

let trustedFolders = {};
try {
  trustedFolders = JSON.parse(fs.readFileSync(trustedFoldersPath, "utf8"));
} catch (_) {
  trustedFolders = {};
}

trustedFolders["/repos"] = "TRUST_FOLDER";

fs.writeFileSync(trustedFoldersPath, JSON.stringify(trustedFolders, null, 2) + "\n");

let projects = {};
try {
  projects = JSON.parse(fs.readFileSync(projectsPath, "utf8"));
} catch (_) {
  projects = {};
}

projects.projects = projects.projects || {};
projects.projects["/app"] = projects.projects["/app"] || "app";

fs.writeFileSync(projectsPath, JSON.stringify(projects, null, 2) + "\n");

let state = {};
try {
  state = JSON.parse(fs.readFileSync(statePath, "utf8"));
} catch (_) {
  state = {};
}

state.tipsShown = state.tipsShown || 1;

fs.writeFileSync(statePath, JSON.stringify(state, null, 2) + "\n");
EOF'

echo "==> Starting bridge as non-root user..."
exec su -m -s /bin/bash bridge -c "cd /app && export HOME=/home/bridge && exec bridge --config $BRIDGE_CONFIG"
