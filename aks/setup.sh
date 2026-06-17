#!/usr/bin/env bash
# End-to-end setup of the zero-trust API gateway demo on an existing AKS cluster.
# Idempotent: safe to re-run. Assumes:
#   * kubectl context points at the AKS cluster
#   * az is logged in to the subscription that owns the cluster
#   * $TRAEFIK_HUB_TOKEN is exported (the only real secret)
#   * helm repos traefik + jetstack are added
#
# Unlike kind, the gateway is exposed via an Azure LoadBalancer bound to a
# reserved Standard public IP, hostnames are <name>.demo.techmasters.cloud
# (wildcard A record -> the LB IP), and TLS is real Let's Encrypt certs issued
# by cert-manager over HTTP-01.
set -euo pipefail
cd "$(dirname "$0")/.."

: "${TRAEFIK_HUB_TOKEN:?export your Traefik Hub token first}"
RG="${AKS_RG:-traefik}"
CLUSTER="${AKS_NAME:-traefik}"
PIP_NAME="${PIP_NAME:-traefik-demo-pip}"
DOMAIN="${DEMO_DOMAIN:-demo.techmasters.cloud}"

echo "==> Reserving / reading static public IP in the AKS node resource group"
NODE_RG="$(az aks show -g "$RG" -n "$CLUSTER" --query nodeResourceGroup -o tsv)"
LOC="$(az aks show -g "$RG" -n "$CLUSTER" --query location -o tsv)"
LB_IP="$(az network public-ip show -g "$NODE_RG" -n "$PIP_NAME" --query ipAddress -o tsv 2>/dev/null || true)"
if [ -z "${LB_IP}" ]; then
  LB_IP="$(az network public-ip create -g "$NODE_RG" -n "$PIP_NAME" \
    --sku Standard --allocation-method Static --location "$LOC" \
    --query publicIp.ipAddress -o tsv)"
fi
SUFFIX="${DOMAIN}"
echo "    LB IP = ${LB_IP}"
echo "    REQUIRED DNS:  *.${DOMAIN}  A  ${LB_IP}"
echo "    NOTE: aks/*.yaml hardcode ${DOMAIN} and the IP; sed-replace if either differs."

echo "==> Traefik Hub license secret"
kubectl create namespace traefik --dry-run=client -o yaml | kubectl apply -f -
kubectl create secret generic license -n traefik \
  --from-literal=token="$TRAEFIK_HUB_TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "==> Traefik Hub (LoadBalancer on the reserved IP)"
helm upgrade --install traefik traefik/traefik -n traefik -f aks/values.yaml --wait --timeout 5m

echo "==> cert-manager + Let's Encrypt ClusterIssuers"
helm upgrade --install cert-manager jetstack/cert-manager -n cert-manager \
  --create-namespace --set crds.enabled=true --wait --timeout 5m
kubectl apply -f aks/cluster-issuer.yaml

echo "==> Observability (LGTM + Alloy + Grafana dashboard)"
kubectl create namespace observability --dry-run=client -o yaml | kubectl apply -f -
kubectl create configmap grafana-traefik -n observability \
  --from-file=traefik.json=observability/grafana/traefik-dashboard.json \
  --from-file=traefik-provider.yaml=observability/grafana/traefik-provider.yaml \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f observability/lgtm.yaml
kubectl apply -f observability/alloy.yaml

echo "==> Keycloak + demo realm"
kubectl create namespace keycloak --dry-run=client -o yaml | kubectl apply -f -
kubectl create configmap keycloak-realm -n keycloak \
  --from-file=realm-demo.json=keycloak/realm-demo.json \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f keycloak/keycloak.yaml

echo "==> Backends, middlewares, AKS ipAllowList, routes + certs"
kubectl create namespace apps --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f apps/whoami.yaml
kubectl apply -f middlewares/
kubectl apply -f aks/ipallowlist.yaml      # overrides kind allowlist with operator public IP
kubectl apply -f aks/instrumented.yaml     # orders/inventory from docker.io/ams0/echo-svc:demo
kubectl apply -f aks/routes.yaml

echo "==> echo-svc image (orders/inventory will ImagePullBackOff until this runs)"
echo "    docker login && ./aks/build-push-echo.sh"

echo "==> Done. Endpoints:"
echo "    API        https://api.${SUFFIX}/{free,pro,enterprise,whoami}"
echo "    Keycloak   https://keycloak.${SUFFIX}"
echo "    Grafana    https://grafana.${SUFFIX}"
echo "    Dashboard  https://traefik.${SUFFIX}/dashboard/"
echo "    Helpers:   source aks/env.sh && ./scripts/call-api.sh pro bob"
