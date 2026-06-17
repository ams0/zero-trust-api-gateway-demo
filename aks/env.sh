# Source this to point the demo helper scripts at the AKS deployment:
#   source aks/env.sh
#   ./scripts/call-api.sh pro bob
#   ./scripts/loadtest.sh free alice 200 20
#
# These override the kind defaults (*.local) baked into the scripts.
# DNS: *.demo.techmasters.cloud -> 135.225.130.22 (Traefik LoadBalancer).
export LB_IP="135.225.130.22"
export SUFFIX="demo.techmasters.cloud"

export API_BASE="https://api.${SUFFIX}"
export KEYCLOAK_URL="https://keycloak.${SUFFIX}"
export GRAFANA_URL="https://grafana.${SUFFIX}"
export TRAEFIK_DASHBOARD_URL="https://traefik.${SUFFIX}/dashboard/"

echo "AKS demo endpoints:"
echo "  API        ${API_BASE}/{free,pro,enterprise,whoami}"
echo "  Keycloak   ${KEYCLOAK_URL}"
echo "  Grafana    ${GRAFANA_URL}"
echo "  Dashboard  ${TRAEFIK_DASHBOARD_URL}"
