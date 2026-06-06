#!/usr/bin/env bash
# e2e test: bridgectl discovers and uses a system daemon via /run/ai-agent-bridge/server.addr
set -euo pipefail

PASS=0
FAIL=0

run_test() {
  local name="$1"
  shift
  if "$@" 2>&1; then
    echo "PASS: $name"
    PASS=$((PASS + 1))
  else
    echo "FAIL: $name"
    FAIL=$((FAIL + 1))
  fi
}

# ---------------------------------------------------------------------------
# Verify the addr file was written by cmd/bridge
# ---------------------------------------------------------------------------
test_addr_file_exists() {
  test -f /run/ai-agent-bridge/server.addr
  addr=$(cat /run/ai-agent-bridge/server.addr)
  test -n "$addr"
  echo "  addr file contains: $addr"
}

run_test "system addr file exists and is non-empty" test_addr_file_exists

# ---------------------------------------------------------------------------
# bridgectl server status discovers daemon via addr file (no user socket)
# ---------------------------------------------------------------------------
test_server_status_running() {
  output=$(bridgectl server status 2>&1)
  echo "$output"
  echo "$output" | grep -q "Server: running"
}

run_test "server status: shows running" test_server_status_running

test_server_status_address() {
  output=$(bridgectl server status 2>&1)
  echo "$output"
  echo "$output" | grep -q "Address"
}

run_test "server status: shows Address field" test_server_status_address

test_server_status_providers() {
  output=$(bridgectl server status 2>&1)
  echo "$output"
  echo "$output" | grep -q "Providers"
}

run_test "server status: shows Providers field" test_server_status_providers

# ---------------------------------------------------------------------------
# bridgectl run --no-tty with echo provider round-trips input
# ---------------------------------------------------------------------------
test_echo_round_trip() {
  # Keep stdin open for 2s after sending the message so the echo has time
  # to travel back before bridgectl sees EOF and stops the session.
  output=$( (printf "PING_FROM_E2E_TEST\n"; sleep 2) | timeout 15 bridgectl run --provider echo --no-tty /tmp 2>&1)
  echo "$output"
  echo "$output" | grep -q "PING_FROM_E2E_TEST"
}

run_test "echo provider: round-trips stdin via system daemon" test_echo_round_trip

# ---------------------------------------------------------------------------
# Result
# ---------------------------------------------------------------------------
echo ""
if [ "$FAIL" -gt 0 ]; then
  echo "SMOKE TEST FAILED ($FAIL failed, $PASS passed)"
  exit 1
fi
echo "SMOKE TEST PASSED ($PASS passed)"
