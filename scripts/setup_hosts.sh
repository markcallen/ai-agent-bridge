#!/usr/bin/env bash
set -euo pipefail

# Ensures bridge.local resolves to 127.0.0.1 in /etc/hosts.
#
# Node.js TLS rejects IP addresses as SNI servernames, so clients connecting
# to the bridge via gRPC must use a hostname that matches the server certificate
# CN/SAN (bridge.local). This script adds the required /etc/hosts entry once.

HOSTS_FILE="/etc/hosts"
HOSTNAME_ENTRY="bridge.local"
HOSTS_LINE="127.0.0.1 ${HOSTNAME_ENTRY}"

# Already present?
if grep -qE "^\s*127\.0\.0\.1\s+.*\b${HOSTNAME_ENTRY}\b" "${HOSTS_FILE}" 2>/dev/null; then
    echo "==> ${HOSTNAME_ENTRY} already in ${HOSTS_FILE} — nothing to do"
    exit 0
fi

echo "==> Adding '${HOSTS_LINE}' to ${HOSTS_FILE}"

if [ "$(id -u)" -eq 0 ]; then
    echo "${HOSTS_LINE}" >> "${HOSTS_FILE}"
elif command -v sudo >/dev/null 2>&1; then
    echo "${HOSTS_LINE}" | sudo tee -a "${HOSTS_FILE}" >/dev/null
else
    echo "WARNING: Cannot update ${HOSTS_FILE} (not root, no sudo)."
    echo "         Add the following line manually:"
    echo "           ${HOSTS_LINE}"
    exit 0
fi

echo "==> Done. ${HOSTNAME_ENTRY} -> 127.0.0.1"
