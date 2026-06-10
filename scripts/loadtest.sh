#!/usr/bin/env bash
# Fire concurrent requests at a route to trip its rate limit. Because the limiter
# keys on the JWT's user id, every request lands in the same bucket -> you see
# 200s give way to 429s. Free trips almost immediately; pro takes far more.
#   ./loadtest.sh [free|pro|enterprise|whoami] [username] [count] [concurrency]
# Note: keep counts modest on a local kind cluster (a few hundred max).
set -uo pipefail
cd "$(dirname "$0")"
TIER="${1:-free}"
USERNAME="${2:-alice}"
COUNT="${3:-40}"
CONC="${4:-20}"
URL="https://api.local/${TIER}"

TOKEN="$(./get-token.sh "${USERNAME}" 2>/dev/null || true)"
if [[ -z "${TOKEN}" || "${TOKEN}" == "null" ]]; then
  echo "ERROR: could not obtain a token for '${USERNAME}'." >&2
  echo "       Is Keycloak reachable and Traefik healthy? Check:" >&2
  echo "       kubectl get pods -n traefik -n keycloak" >&2
  exit 1
fi

echo "Firing ${COUNT} requests (${CONC} concurrent) at /${TIER} as ${USERNAME}..."
seq 1 "${COUNT}" | xargs -P "${CONC}" -I{} \
  sh -c "curl -sk -o /dev/null -w '%{http_code}\n' -H \"Authorization: Bearer ${TOKEN}\" \"${URL}\" || echo 000" \
  | sort | uniq -c | sed 's/^/   /'
echo "(200 = served, 429 = rate limited, 000 = connection failed)"
