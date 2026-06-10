#!/usr/bin/env bash
# Fetch a Keycloak access token via the password grant.
#   ./get-token.sh [username] [password]
# Users: alice (free), bob (pro), carol (enterprise). Password: password
set -euo pipefail
USERNAME="${1:-alice}"
PASSWORD="${2:-password}"
KC="${KEYCLOAK_URL:-https://keycloak.local}"

curl -sk "${KC}/realms/demo/protocol/openid-connect/token" \
  -d grant_type=password \
  -d client_id=gateway-demo \
  -d client_secret=gateway-demo-secret \
  -d username="${USERNAME}" \
  -d password="${PASSWORD}" | jq -r .access_token
