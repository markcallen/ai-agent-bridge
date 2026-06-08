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

# Mirror what systemd RuntimeDirectory=bridge does: create and own
# the runtime dir so the bridge process can write the system addr file.
mkdir -p /run/bridge
chown bridge:bridge /run/bridge

# Ensure bridge user can read/write mounted workspace volumes such as /repos.
for _vol in /repos /workspace; do
  if [ -d "$_vol" ]; then
    chown bridge:bridge "$_vol"
  fi
done

if [ ! -f "$CERT_DIR/ca.crt" ]; then
  echo "==> Initializing CA..."
  ai-agent-bridge-ca init --name ai-agent-bridge-ca --out "$CERT_DIR"

  echo "==> Issuing server certificate..."
  ai-agent-bridge-ca issue --type server --cn "$BRIDGE_CN" \
    --san "$BRIDGE_SANS" \
    --ca "$CERT_DIR/ca.crt" --ca-key "$CERT_DIR/ca.key" \
    --out "$CERT_DIR"

  echo "==> Issuing client certificate..."
  ai-agent-bridge-ca issue --type client --cn "$BRIDGE_CLIENT_CN" \
    --ca "$CERT_DIR/ca.crt" --ca-key "$CERT_DIR/ca.key" \
    --out "$CERT_DIR"

  echo "==> Generating JWT signing keypair..."
  ai-agent-bridge-ca jwt-keygen --out "$CERT_DIR/jwt-signing"

  echo "==> Building trust bundle..."
  ai-agent-bridge-ca bundle --out "$CERT_DIR/ca-bundle.crt" "$CERT_DIR/ca.crt"

  chmod 644 "$CERT_DIR"/*
fi

cp -f "$CERT_DIR"/* "$RUNTIME_CERT_DIR"/
chown -R bridge:bridge "$RUNTIME_CERT_DIR"
chmod 644 "$RUNTIME_CERT_DIR"/*.crt "$RUNTIME_CERT_DIR"/*.pub
chmod 600 "$RUNTIME_CERT_DIR"/*.key

if [ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]; then
  echo "==> Verifying Claude API-key auth..."
  CLAUDE_AUTH_STATUS="$(su -m -s /bin/bash bridge -c 'cd /app && export HOME=/home/bridge && ./node_modules/.bin/claude auth status' || true)"
  if [[ "$CLAUDE_AUTH_STATUS" != *'"loggedIn": true'* ]] || [[ "$CLAUDE_AUTH_STATUS" != *'"apiKeySource": "CLAUDE_CODE_OAUTH_TOKEN"'* ]]; then
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

// Pre-trust the e2e repo path so the trust dialog is suppressed
state.projects = state.projects || {};
const trustedPaths = ["/tmp/ai-agent-bridge"];
for (const p of trustedPaths) {
  state.projects[p] = state.projects[p] || {};
  state.projects[p].hasTrustDialogAccepted = true;
  state.projects[p].projectOnboardingSeenCount = 1;
}

// Pre-approve the CLAUDE_CODE_OAUTH_TOKEN so the "use this API key?" dialog is suppressed.
// Claude stores the last 20 chars of the key as the approval token.
const apiKey = process.env.CLAUDE_CODE_OAUTH_TOKEN || "";
if (apiKey) {
  const keyToken = apiKey.slice(-20);
  state.customApiKeyResponses = state.customApiKeyResponses || {};
  const approved = state.customApiKeyResponses.approved || [];
  if (!approved.includes(keyToken)) approved.push(keyToken);
  state.customApiKeyResponses.approved = approved;
  state.customApiKeyResponses.rejected = (state.customApiKeyResponses.rejected || []).filter(k => k !== keyToken);
}

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

// Disable auto-update notifications so Gemini does not try to update during e2e tests
settings.general = settings.general || {};
settings.general.enableAutoUpdateNotification = false;
settings.general.autoUpdate = false;

fs.writeFileSync(settingsPath, JSON.stringify(settings, null, 2) + "\n");

let trustedFolders = {};
try {
  trustedFolders = JSON.parse(fs.readFileSync(trustedFoldersPath, "utf8"));
} catch (_) {
  trustedFolders = {};
}

trustedFolders["/tmp/ai-agent-bridge"] = "TRUST_FOLDER";

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

echo "==> Seeding Codex onboarding state..."
su -m -s /bin/bash bridge -c 'cd /app && export HOME=/home/bridge && node <<'\''EOF'\''
const fs = require("fs");
const path = require("path");

const codexDir = path.join(process.env.HOME, ".codex");
const authPath = path.join(codexDir, "auth.json");
const configPath = path.join(codexDir, "config.toml");

fs.mkdirSync(codexDir, { recursive: true });

const apiKey = process.env.OPENAI_API_KEY || "";
if (apiKey) {
  fs.writeFileSync(
    authPath,
    JSON.stringify(
      {
        auth_mode: "apikey",
        OPENAI_API_KEY: apiKey,
      },
      null,
      2,
    ) + "\n",
  );
}

const configToml = [
  "model = \"gpt-5.4\"",
  "",
  "[projects.\"/repos\"]",
  "trust_level = \"trusted\"",
  "",
  "[projects.\"/repos/penduin\"]",
  "trust_level = \"trusted\"",
  "",
  "[notice.model_migrations]",
  "\"gpt-5.3-codex\" = \"gpt-5.4\"",
  "",
].join("\n");

fs.writeFileSync(configPath, configToml);
EOF'

echo "==> Starting bridge as non-root user..."
exec su -m -s /bin/bash bridge -c "cd /app && export HOME=/home/bridge && exec ai-agent-bridge --config $BRIDGE_CONFIG"
