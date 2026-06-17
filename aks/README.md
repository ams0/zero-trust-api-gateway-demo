# Running the demo on AKS

The repo's main [README](../README.md) deploys to a local **kind** cluster. This
directory adapts the *same* manifests to a real **AKS** cluster. The security
model, middlewares, Keycloak realm, and observability stack are identical — only
the edges that touch infrastructure change.

## What's different from kind

| Concern | kind | AKS (this dir) |
|---|---|---|
| Gateway exposure | `NodePort` + host port-map | `LoadBalancer` on a **reserved Standard public IP** |
| Hostnames | `*.local` via `/etc/hosts` | `*.demo.techmasters.cloud` (wildcard A record) |
| TLS | Traefik self-signed | **Let's Encrypt** via cert-manager (HTTP-01) |
| `echo-svc` image | built + side-loaded | **pulled** from `docker.io/ams0/echo-svc:demo` |
| Client source IP | Docker bridge | real caller IP (`externalTrafficPolicy: Local`) |
| Enterprise allowlist | kind bridge CIDR | operator public IP (`aks/ipallowlist.yaml`) |

Everything else (`middlewares/`, `keycloak/`, `observability/`, `apps/whoami.yaml`)
is applied unchanged — JWKS, OTLP endpoints, and Loki push all use in-cluster DNS.

## DNS + endpoints

LB IP `135.225.130.22` (PIP `traefik-demo-pip` in the AKS node resource group).
Create a single wildcard record:

```
*.demo.techmasters.cloud   A   135.225.130.22
```

- API        `https://api.demo.techmasters.cloud/{free,pro,enterprise,whoami}`
- Keycloak   `https://keycloak.demo.techmasters.cloud`
- Grafana    `https://grafana.demo.techmasters.cloud`
- Dashboard  `https://traefik.demo.techmasters.cloud/dashboard/`

> The IP and domain are hardcoded in `aks/values.yaml` and `aks/routes.yaml`. If
> either changes, `sed -i '' 's/135.225.130.22/<new-ip>/g; s/demo.techmasters.cloud/<new-domain>/g' aks/*.yaml`
> (and update `aks/env.sh`) before applying.

## One-shot setup

```bash
export TRAEFIK_HUB_TOKEN="<your-hub-token>"
helm repo add traefik https://traefik.github.io/charts
helm repo add jetstack https://charts.jetstack.io
helm repo update
./aks/setup.sh                 # everything except the echo-svc image

docker login                   # docker.io/ams0
./aks/build-push-echo.sh       # build amd64 echo-svc, push, restart orders/inventory
```

`setup.sh` is idempotent. The files map to setup steps:

- `values.yaml` — Traefik Hub Helm values (LoadBalancer + static PIP, `kubernetesIngress`
  enabled for the ACME solver, dashboard + TLS on the real host).
- `cluster-issuer.yaml` — Let's Encrypt prod + staging `ClusterIssuer`s (HTTP-01).
- `routes.yaml` — `IngressRoute`s + cert-manager `Certificate`s (one TLS secret per host).
- `ipallowlist.yaml` — enterprise `ipAllowList` scoped to the operator's public IP.
- `instrumented.yaml` — `orders`/`inventory` pointing at the Docker Hub image.
- `env.sh` — `source` it to point `scripts/*.sh` at the AKS hosts.

## Verify

```bash
source aks/env.sh

./scripts/call-api.sh free --anon          # 401 — JWT enforced at the edge
./scripts/call-api.sh whoami alice         # injected X-User-* headers, no Authorization
./scripts/call-api.sh pro bob              # 200 from orders -> inventory (real trace)
./scripts/loadtest.sh free alice 60 20     # 200s give way to 429s (per-user rate limit)
./scripts/call-api.sh enterprise carol     # 200 (your IP is allow-listed)
```

Then open Grafana for the gateway→orders→inventory trace and the Traefik dashboard.

### Demoing enterprise network policy

Flip `aks/ipallowlist.yaml` to a non-matching range, re-apply, and the enterprise
route returns **403** while free/pro keep serving:

```bash
sed -i '' 's#94.68.48.29/32#10.99.0.0/16#' aks/ipallowlist.yaml
kubectl apply -f aks/ipallowlist.yaml
./scripts/call-api.sh enterprise carol     # 403
```

## Teardown

```bash
helm uninstall traefik -n traefik
helm uninstall cert-manager -n cert-manager
kubectl delete ns apps keycloak observability traefik cert-manager
az network public-ip delete -g "$(az aks show -g traefik -n traefik --query nodeResourceGroup -o tsv)" -n traefik-demo-pip
# then delete the AKS cluster itself if you're done.
```
