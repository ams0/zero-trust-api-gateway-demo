#!/usr/bin/env bash
# Call a tier endpoint with a freshly-minted token, showing response headers.
#   ./call-api.sh [free|pro|enterprise] [username]
# Run with no token to see the 401:  ./call-api.sh free --anon
set -euo pipefail
cd "$(dirname "$0")"
TIER="${1:-free}"
USERNAME="${2:-alice}"
# API_BASE defaults to the kind host; override for AKS (e.g. source aks/env.sh).
URL="${API_BASE:-https://api.local}/${TIER}"

# call-api.sh makes ONE request (to inspect headers/body). For load, use loadtest.sh.
if [[ -n "${3:-}" ]]; then
  echo "note: call-api.sh sends a single request; for load use:" >&2
  echo "      ./scripts/loadtest.sh ${TIER} ${USERNAME} ${3} ${4:-10}" >&2
fi

if [[ "${USERNAME}" == "--anon" ]]; then
  echo "──────────────────────────────────────────────"
  echo "  GET  ${URL}"
  echo "  Auth: none (anonymous — expect 401)"
  echo "──────────────────────────────────────────────"
  curl -sk -D - -o /dev/null "${URL}"
  exit 0
fi

TOKEN="$(./get-token.sh "${USERNAME}")"
echo "──────────────────────────────────────────────"
echo "  GET  ${URL}"
echo "  Auth: Bearer JWT for user '${USERNAME}'"
echo "        ${TOKEN:0:24}…${TOKEN: -8}"
echo "──────────────────────────────────────────────"
curl -sk -D - -H "Authorization: Bearer ${TOKEN}" "${URL}"
