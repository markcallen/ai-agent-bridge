#!/usr/bin/env bash
# smoke-apt-profile.sh — apt package smoke test for the ai-desktops profile.
#
# Verifies that the ai-desktops deployment profile works end-to-end:
#   1. Install the ai-agent-bridge apt package.
#   2. Apply a fixture config that mirrors the ai-desktops profile structure:
#      - persistence enabled
#      - /workspace in allowed_paths
#      - a deterministic fixture provider (cat) — no API keys required
#   3. Create /workspace/smoke-repo.
#   4. Start the daemon with the fixture config.
#   5. Run the profile-smoke binary to verify:
#      - bridge health
#      - fixture provider registered
#      - session start for /workspace/smoke-repo passes path policy
#      - input written to the session is echoed back
#   6. Restart the daemon.
#   7. Verify health is restored (persistence not corrupted).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
GNUPGHOME="$TMP_DIR/gnupg"
REPO_DIR="$TMP_DIR/repo"
PACKAGES_DIR="$TMP_DIR/packages"
HEALTHCHECK_BIN="$TMP_DIR/plain-healthcheck"
PROFILE_SMOKE_BIN="$TMP_DIR/profile-smoke"
SUITE="${SUITE:-noble}"
: "${GOCACHE:=/tmp/ai-agent-bridge-go-build}"
: "${GOFLAGS:=-buildvcs=false}"

export GOCACHE
export GOFLAGS

cleanup() {
  local rc=$?
  docker rm -f "apt-smoke-profile-$SUITE" >/dev/null 2>&1 || true
  rm -rf "$TMP_DIR"
  exit "$rc"
}

trap cleanup EXIT

# ── Build the .deb package ─────────────────────────────────────────────────
mkdir -p "$GNUPGHOME"
chmod 700 "$GNUPGHOME"

gpg --batch \
  --homedir "$GNUPGHOME" \
  --pinentry-mode loopback \
  --passphrase '' \
  --quick-generate-key \
  "AI Agent Bridge Smoke <smoke@ai-agent-bridge.local>" \
  rsa3072 sign 0

export GNUPGHOME
VERSION="0.0.0-smoke" OUTPUT_DIR="$PACKAGES_DIR" "$ROOT_DIR/scripts/build-deb.sh"
REPO_DIR="$REPO_DIR" PACKAGES_DIR="$PACKAGES_DIR" "$ROOT_DIR/scripts/build-apt-repo.sh"

# ── Build helper binaries ─────────────────────────────────────────────────
go build -o "$HEALTHCHECK_BIN" ./e2e/cmd/plain-healthcheck
go build -o "$PROFILE_SMOKE_BIN" ./e2e/cmd/profile-smoke

# ── Write the fixture bridge config ───────────────────────────────────────
FIXTURE_CONFIG="$TMP_DIR/bridge-fixture.yaml"
cat >"$FIXTURE_CONFIG" <<'EOF'
# Fixture config for ai-desktops profile smoke test.
# Mirrors the ai-desktops profile structure but uses /bin/cat as the
# provider so no Node.js or API keys are required in CI.

server:
  listen: "127.0.0.1:9445"

tls:
  ca_bundle: ""
  cert: ""
  key: ""

auth:
  jwt_public_keys: []
  jwt_audience: "bridge"
  jwt_max_ttl: "5m"

feature_flags:
  provider_fallbacks: false

sessions:
  max_per_project: 5
  max_global: 20
  idle_timeout: "30m"
  stop_grace_period: "5s"
  event_buffer_size: 10000
  max_subscribers_per_session: 10
  subscriber_ttl: "30m"

input:
  max_size_bytes: 65536

rate_limits:
  global_rps: 50
  global_burst: 100
  start_session_per_client_rps: 1
  start_session_per_client_burst: 3
  send_input_per_session_rps: 5
  send_input_per_session_burst: 20

persistence:
  db_path: "/var/lib/ai-agent-bridge/sessions.db"

providers:
  fixture:
    binary: "/bin/cat"
    args: []
    startup_timeout: "5s"
    validate_startup: false

allowed_paths:
  - "/workspace"
  - "/tmp"

logging:
  level: "info"
  format: "json"
EOF

# ── Determine Ubuntu image tag for suite ──────────────────────────────────
case "$SUITE" in
  noble)  IMAGE_TAG="24.04" ;;
  plucky) IMAGE_TAG="25.04" ;;
  *)
    echo "smoke-apt-profile: unsupported SUITE=$SUITE" >&2
    exit 1
    ;;
esac

CONTAINER="apt-smoke-profile-$SUITE"

docker run -d \
  --name "$CONTAINER" \
  --add-host=host.docker.internal:host-gateway \
  -e SUITE="$SUITE" \
  -v "$REPO_DIR:/opt/aptrepo:ro" \
  -v "$HEALTHCHECK_BIN:/usr/local/bin/plain-healthcheck:ro" \
  -v "$PROFILE_SMOKE_BIN:/usr/local/bin/profile-smoke:ro" \
  -v "$FIXTURE_CONFIG:/tmp/bridge-fixture.yaml:ro" \
  ubuntu:"$IMAGE_TAG" \
  bash -lc "$(cat <<'INNER'
    set -euo pipefail
    export DEBIAN_FRONTEND=noninteractive

    # Install package.
    apt-get update -q
    apt-get install -y -q ca-certificates gnupg
    install -d /etc/apt/keyrings
    gpg --dearmor -o /etc/apt/keyrings/ai-agent-bridge.gpg \
      /opt/aptrepo/ai-agent-bridge-archive-keyring.asc
    echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/ai-agent-bridge.gpg] file:/opt/aptrepo ${SUITE} main" \
      > /etc/apt/sources.list.d/ai-agent-bridge.list
    apt-get update -q
    apt-get install -y -q ai-agent-bridge

    # Apply fixture config.
    cp /tmp/bridge-fixture.yaml /etc/ai-agent-bridge/bridge.yaml

    # Create workspace directory.
    mkdir -p /workspace/smoke-repo

    # Start the daemon in the background.
    /usr/bin/ai-agent-bridge --config /etc/ai-agent-bridge/bridge.yaml \
      >/tmp/bridge.log 2>&1 &
    BRIDGE_PID=$!

    # Wait for bridge to report healthy.
    for i in $(seq 1 30); do
      if /usr/local/bin/plain-healthcheck -target "127.0.0.1:9445" >/dev/null 2>&1; then
        break
      fi
      if ! kill -0 "$BRIDGE_PID" 2>/dev/null; then
        echo "PROFILE SMOKE FAILED: bridge exited early"
        cat /tmp/bridge.log >&2
        exit 1
      fi
      sleep 1
    done

    # Keep container alive for external smoke probe.
    tail -f /dev/null
INNER
  )" >/dev/null

# ── Wait for bridge health from host ──────────────────────────────────────
echo "==> Waiting for bridge health (suite=$SUITE)"
for _ in $(seq 1 30); do
  if docker exec "$CONTAINER" /usr/local/bin/plain-healthcheck \
      -target "127.0.0.1:9445" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! docker exec "$CONTAINER" /usr/local/bin/plain-healthcheck \
    -target "127.0.0.1:9445" >/dev/null 2>&1; then
  docker exec "$CONTAINER" sh -c 'cat /tmp/bridge.log' >&2 || true
  echo "APT PROFILE SMOKE FAILED: bridge not healthy suite=$SUITE" >&2
  exit 1
fi

# ── Run profile smoke (provider session + echo test) ──────────────────────
echo "==> Running profile smoke (provider session, workspace path, echo)"
if ! docker exec "$CONTAINER" /usr/local/bin/profile-smoke \
    -target "127.0.0.1:9445" \
    -provider "fixture" \
    -repo-path "/workspace/smoke-repo" \
    -timeout "30s"; then
  docker exec "$CONTAINER" sh -c 'cat /tmp/bridge.log' >&2 || true
  echo "APT PROFILE SMOKE FAILED: profile-smoke binary failed suite=$SUITE" >&2
  exit 1
fi

# ── Restart bridge and verify persistence ─────────────────────────────────
echo "==> Restarting bridge to verify persistence"
docker exec "$CONTAINER" bash -c '
  BRIDGE_PID=$(pgrep -x ai-agent-bridge || true)
  if [ -n "$BRIDGE_PID" ]; then
    kill "$BRIDGE_PID"
    for i in $(seq 1 10); do
      pgrep -x ai-agent-bridge >/dev/null || break
      sleep 1
    done
  fi
  /usr/bin/ai-agent-bridge --config /etc/ai-agent-bridge/bridge.yaml \
    >>/tmp/bridge.log 2>&1 &
'

for _ in $(seq 1 30); do
  if docker exec "$CONTAINER" /usr/local/bin/plain-healthcheck \
      -target "127.0.0.1:9445" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! docker exec "$CONTAINER" /usr/local/bin/plain-healthcheck \
    -target "127.0.0.1:9445" >/dev/null 2>&1; then
  docker exec "$CONTAINER" sh -c 'cat /tmp/bridge.log' >&2 || true
  echo "APT PROFILE SMOKE FAILED: bridge did not recover after restart suite=$SUITE" >&2
  exit 1
fi

echo "APT PROFILE SMOKE PASSED: suite=$SUITE"
