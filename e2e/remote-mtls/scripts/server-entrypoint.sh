#!/usr/bin/env bash
set -euo pipefail

# Run the normal docker entrypoint first (generates PKI, seeds onboarding,
# starts the bridge server in the background).
# We source it indirectly by calling the original entrypoint, but we need
# the server to be running before we can issue client creds. So we start
# the server in the background via the original entrypoint, wait for it
# to be healthy, issue client creds, then wait for the server to exit.

# Run the original entrypoint in the background.
/app/entrypoint.sh &
SERVER_PID=$!

echo "==> Waiting for bridge server to be ready..."
for i in $(seq 1 60); do
  if bash -c '</dev/tcp/127.0.0.1/9445' 2>/dev/null; then
    echo "    Bridge server is ready."
    break
  fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "ERROR: bridge server exited before becoming ready" >&2
    wait "$SERVER_PID"
    exit $?
  fi
  sleep 1
done

CLIENT_NAME="${ISSUE_CLIENT_NAME:-remote-client}"
echo "==> Issuing client credentials for ${CLIENT_NAME}..."

# Run as bridge user since the state dir is owned by bridge.
su -m -s /bin/bash bridge -c "
  export HOME=/home/bridge
  bridgectl server issue-client --name '${CLIENT_NAME}' --bundle
"

# Copy the bundle to the shared client-creds volume.
BUNDLE_SRC="/home/bridge/.config/bridgectl/certs/clients/${CLIENT_NAME}/${CLIENT_NAME}-creds.tar.gz"
if [ -f "$BUNDLE_SRC" ]; then
  cp "$BUNDLE_SRC" /client-creds/
  echo "==> Client credential bundle copied to /client-creds/"
  ls -la /client-creds/
else
  echo "ERROR: bundle not found at $BUNDLE_SRC" >&2
  ls -la "/home/bridge/.config/bridgectl/certs/clients/${CLIENT_NAME}/" 2>/dev/null || true
fi

# Wait for the bridge server process.
echo "==> Server running (pid $SERVER_PID), waiting..."
wait "$SERVER_PID"
