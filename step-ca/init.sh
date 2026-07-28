#!/bin/sh
set -eu

# Step CA initialization script for local development.
# This runs once to bootstrap the CA, then subsequent starts skip init.
# Uses /bin/sh for Alpine compatibility (smallstep/step-ca base image).

STEPPATH="${STEPPATH:-/home/step}"
SHARED_DIR="${SHARED_DIR:-/home/step/shared}"
CA_NAME="${CA_NAME:-AI Agent Bridge Dev CA}"
CA_DNS="${CA_DNS:-step-ca.local,localhost}"
# Strip trailing/leading commas from CA_DNS (env var composition may leave them).
CA_DNS="$(echo "$CA_DNS" | sed 's/^,*//;s/,*$//;s/,,*/,/g')"
CA_ADDRESS="${CA_ADDRESS:-:9443}"
PROVISIONER_PASSWORD_FILE="${STEPPATH}/secrets/password"
PROVISIONER_PASSWORD="${PROVISIONER_PASSWORD:-step-ca-dev-password}"

# If already initialized, just export the root cert and exit.
if [ -f "${STEPPATH}/config/ca.json" ]; then
  echo "==> Step CA already initialized, exporting root cert"
  mkdir -p "${SHARED_DIR}"
  cp "${STEPPATH}/certs/root_ca.crt" "${SHARED_DIR}/root_ca.crt"
  chmod 644 "${SHARED_DIR}/root_ca.crt"
  echo "==> Root cert exported to ${SHARED_DIR}/root_ca.crt"
  exit 0
fi

echo "==> Initializing Step CA: ${CA_NAME}"

# Create password file for the JWK provisioner.
mkdir -p "${STEPPATH}/secrets"
printf '%s' "${PROVISIONER_PASSWORD}" > "${PROVISIONER_PASSWORD_FILE}"
chmod 600 "${PROVISIONER_PASSWORD_FILE}"

# Initialize the CA with a JWK provisioner.
# --deployment-type standalone: no remote management
# --name: CA display name
# --dns: SANs for the CA server certificate
# --address: listen address
# --provisioner: name of the default JWK provisioner
# --password-file: password for the JWK provisioner and CA keys
step ca init \
  --deployment-type standalone \
  --name "${CA_NAME}" \
  --dns "${CA_DNS}" \
  --address "${CA_ADDRESS}" \
  --provisioner "bridge-jwk" \
  --password-file "${PROVISIONER_PASSWORD_FILE}"

# Increase default certificate max duration to 90 days (for server certs).
# The default JWK provisioner allows 24h max which is too short. We patch
# ca.json directly since the CA is not running yet.
#
# The step-ca image includes jq, so we use it to patch the provisioner claims.
CA_JSON="${STEPPATH}/config/ca.json"
if command -v jq >/dev/null 2>&1; then
  jq '(.authority.provisioners[] | select(.name == "bridge-jwk") | .claims) = {
    "maxTLSCertDuration": "2160h",
    "defaultTLSCertDuration": "2160h"
  }' "${CA_JSON}" > "${CA_JSON}.tmp" && mv "${CA_JSON}.tmp" "${CA_JSON}"
  echo "==> Patched JWK provisioner: maxTLSCertDuration=2160h"
else
  echo "WARN: jq not available, skipping provisioner duration patch"
  echo "      Server certs will be limited to 24h validity"
fi

# Export the root certificate to the shared volume so the bridge can read it.
mkdir -p "${SHARED_DIR}"
cp "${STEPPATH}/certs/root_ca.crt" "${SHARED_DIR}/root_ca.crt"
chmod 644 "${SHARED_DIR}/root_ca.crt"

echo "==> Step CA initialized successfully"
echo "    Root cert: ${SHARED_DIR}/root_ca.crt"
echo "    Address: ${CA_ADDRESS}"
echo "    Provisioner: bridge-jwk (JWK)"
