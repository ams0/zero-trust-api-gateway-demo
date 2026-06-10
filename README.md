# Zero-Trust API Gateway Demo — Traefik Hub on Kubernetes

A working demo that positions **Traefik Hub** as the single enforcement point in
front of a set of lightweight microservices. All security policy — authentication,
per-tier rate limiting, response hardening, and network allowlisting — lives at the
gateway. The backend services (`traefik/whoami`) contain **zero** auth code.

Observability is end-to-end: **metrics, traces, and logs** flow into a single
Grafana pane, alongside Traefik's built-in dashboard.

> **⚠️ Demo credentials — throwaway, not for production.** Every credential in this
> repo is a deliberately fake local-demo value: Keycloak admin `admin/admin`, the
> realm client secret `gateway-demo-secret`, user passwords `password`
> (`alice`/`bob`/`carol`), and anonymous-admin Grafana. They exist so the demo runs
> with one command. **Never reuse them anywhere real.** Your **Traefik Hub license
> token** is the only real secret — it is supplied at runtime via the
> `TRAEFIK_HUB_TOKEN` environment variable and is **never** committed to this repo.

---

## What gets deployed

| Component | Role |
|---|---|
| **kind** cluster (`zero-trust-gw`) | Local Kubernetes, publishes host ports 80/443 |
| **Traefik Hub** API Gateway | The edge: routing + all security middleware |
| **Keycloak** (`demo` realm) | Identity provider issuing the JWTs |
| **whoami ×3** (`free`/`pro`/`enterprise`) | Backend "microservices", one per tier |
| **orders + inventory** | OTel-instrumented backends behind `/orders` (real distributed trace) |
| **grafana/otel-lgtm** | All-in-one OTel Collector + Prometheus + Tempo + Loki + Grafana |
| **Grafana Alloy** | Ships Traefik access logs into Loki |

### Request path

```
                                  ┌──────────────────────── Traefik Hub (edge) ────────────────────────┐
  client ──https──▶  :443  ─────▶ │  chain-<tier>:                                                      │
   (Bearer JWT)                   │   [ipAllowList]→ jwt-auth → security-headers → rateLimit → strip │ ─▶ whoami-<tier>
                                  └─────────┬───────────────────────────┬───────────────────────────────┘
                                            │ JWKS (verify signature)    │ OTLP gRPC :4317 (metrics+traces)
                                            ▼                            ▼
                                        Keycloak                   otel-lgtm ◀── Alloy (access logs → Loki)
                                                                       │
                                                                    Grafana
```

All three tiers are the **same API** (`orders → inventory`, instrumented) at
different quotas. They share one host (`api.local`) and differ only by path
prefix and the **chain middleware** attached. Because the backend is
instrumented, **every tier call produces a distributed trace**.

| Tier | Path | Auth | Rate limit (per user) | Backend | Extra |
|---|---|---|---|---|---|
| Free | `/free` | JWT | 5 req/s, burst 10 | orders→inventory | security headers |
| Pro | `/pro` | JWT | 50 req/s, burst 100 | orders→inventory | security headers |
| Enterprise | `/enterprise` | JWT | 500 req/s, burst 1000 | orders→inventory | security headers **+ IP allowlist** |

`/whoami` is a separate **inspection** endpoint (JWT + headers, no rate limit) →
the whoami echo, for showing injected `X-User-*` headers and the stripped
`Authorization` at the backend. It is not a tier.

---

## Prerequisites

- Docker, [`kind`](https://kind.sigs.k8s.io/), `kubectl`, `helm`, `jq`, `curl`
- A **Traefik Hub** license token (free tier works for this demo)
- Get the token: <https://hub.traefik.io> → **Gateways** → add a gateway → copy the token

```bash
export TRAEFIK_HUB_TOKEN="<your-hub-token>"
```

---

## Deploy (run these manually before the demo)

> The only slow, network-bound step is **step 1**. Do it early; everything after is fast.

### 1. Pre-pull all images (slow — do this ahead of time)
```bash
./scripts/00-prepull-images.sh
```
Pulls every workload image listed in `scripts/images.txt` into your local Docker cache.

> **kind node image:** by default the scripts let `kind` use the node image bundled
> with your installed `kind` version — always compatible, no guessing. `kindest/node`
> tags are published per-kind-release (not arbitrary k8s versions), so a tag like
> `v1.36.x` only works if your `kind` supports it. To pin a specific Kubernetes version,
> set `KIND_NODE_IMAGE` (it's then pre-pulled and used at create time):
> ```bash
> export KIND_NODE_IMAGE=kindest/node:v1.36.1   # check `kind` release notes for valid tags
> ```

### 2. Create the kind cluster
```bash
kind create cluster --config kind/kind-config.yaml
```
Creates the `zero-trust-gw` cluster and publishes host `80→30080`, `443→30443`.

### 3. Side-load the cached images (so no pod pulls from the internet)
```bash
ARCH="linux/$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/;s/arm64/arm64/')"
grep -vE '^\s*(#|$)' scripts/images.txt | while read -r img; do
  echo "==> loading $img"
  docker image save --platform "$ARCH" "$img" -o /tmp/kind-img.tar
  kind load image-archive /tmp/kind-img.tar --name zero-trust-gw
done
rm -f /tmp/kind-img.tar
```

> **Why `save --platform … | load image-archive` instead of `kind load docker-image`?**
> Docker Desktop's **containerd image store** keeps images as multi-arch manifest
> *lists* but only pulls your host platform's layers. `kind load docker-image` tries
> to import *all* referenced platforms and fails with `content digest … not found`.
> Exporting a single-platform archive first avoids that. (If you've disabled the
> containerd image store, plain `kind load docker-image "$img" --name zero-trust-gw`
> also works.)

### 4. Map the demo hostnames to localhost
Add this line to `/etc/hosts`:
```
127.0.0.1  api.local keycloak.local grafana.local traefik.local
```

### 5. Create the Traefik Hub license secret
```bash
export TRAEFIK_HUB_TOKEN="<your-hub-token>"
kubectl create namespace traefik
kubectl create secret generic license -n traefik \
  --from-literal=token="$TRAEFIK_HUB_TOKEN"
```

### 6. Deploy the observability stack (LGTM + Alloy + Grafana dashboard)
The LGTM pod mounts the Traefik dashboard from a ConfigMap, so create it first.
```bash
kubectl create namespace observability
kubectl create configmap grafana-traefik -n observability \
  --from-file=traefik.json=observability/grafana/traefik-dashboard.json \
  --from-file=traefik-provider.yaml=observability/grafana/traefik-provider.yaml
kubectl apply -f observability/lgtm.yaml
kubectl apply -f observability/alloy.yaml
```

### 7. Deploy Keycloak with the demo realm
```bash
kubectl create namespace keycloak
kubectl create configmap keycloak-realm -n keycloak \
  --from-file=realm-demo.json=keycloak/realm-demo.json
kubectl apply -f keycloak/keycloak.yaml
```

### 8. Deploy the backend services
```bash
# whoami x3 (the rate-limit / identity-injection tiers)
kubectl apply -f apps/whoami.yaml

# Instrumented backends for the distributed-trace demo (gateway -> orders -> inventory).
# Build the image and side-load it into the cluster, then deploy:
./scripts/build-instrumented.sh
kubectl apply -f apps/instrumented.yaml
```

### 9. Install Traefik Hub via Helm
```bash
helm repo add --force-update traefik https://traefik.github.io/charts
helm repo update
helm install traefik traefik/traefik \
  --namespace traefik \
  --values traefik/values.yaml \
  --wait --timeout 5m
```

### 10. Apply the middlewares and routes
```bash
kubectl apply -f middlewares/
kubectl apply -f routes/
```

### 11. Wait for everything to be ready
```bash
kubectl rollout status deploy/lgtm      -n observability --timeout=300s
kubectl rollout status deploy/keycloak  -n keycloak      --timeout=300s
kubectl rollout status deploy/orders    -n apps          --timeout=120s
kubectl rollout status deploy/inventory -n apps          --timeout=120s
kubectl get pods -A
```

---

## Verify it works

Each step shows the command, what you should see, and why it matters.

**1. Anonymous request is rejected at the edge.**
```bash
./scripts/call-api.sh free --anon
```
- *Expect:* `HTTP/2 401`, empty body.
- *Why:* the JWT middleware rejects the request **before** routing — the backend is never contacted. Auth lives at the gateway, not in the service.

**2. Authenticated tier call succeeds and is traced end-to-end.**
```bash
./scripts/call-api.sh pro bob
```
- *Expect:* `HTTP/2 200` and JSON like
  `{"service":"orders","downstream":{"service":"inventory",...},"user":"bob","user_groups":"pro"}`.
  The `traceparent` on `orders` and on `inventory` share the **same** trace id.
- *Why:* the gateway validated the JWT, injected the caller's identity, and the request fanned out `orders → inventory` as one distributed trace — with zero tracing/auth code in the services.

**3. The backend receives trusted identity, not the credential.**
```bash
./scripts/call-api.sh whoami alice
```
- *Expect:* whoami echoes `X-User-Id`, `X-User-Name: alice`, `X-User-Groups: free`, and **no `Authorization` header**.
- *Why:* the gateway projects verified JWT claims into headers the service can trust and **strips the token** — the service never parses a JWT or sees the credential.

**4. The free tier's rate limit trips.**
```bash
./scripts/loadtest.sh free alice
```
- *Expect:* a mix of `200` and `429` (free = 5 req/s, burst 10).
- *Why:* per-tier quota enforced at the edge, keyed on the JWT's user id (per-user, not per-IP).

**5. The pro tier absorbs the same load.**
```bash
./scripts/loadtest.sh pro bob 40 20
```
- *Expect:* all `200` (pro = 50 req/s).
- *Why:* identical code path, different quota. Moving a customer between tiers is a one-line route change — no service redeploy.

Open in a browser (accept the self-signed cert):
- **Traefik dashboard** → <https://traefik.local/dashboard/>
- **Grafana** → <https://grafana.local>
  - **Metrics dashboard**: Dashboards → *Traefik — Zero-Trust Gateway* (request rate by status code, by tier, latency p50/p95/p99)
  - **Traces**: Explore → **Tempo** → Search → run query → open a tier trace (e.g. a `/pro`
    call) to see the `traefik → orders → inventory` waterfall. (`/whoami` shows a single
    Traefik edge span — whoami isn't instrumented; orders/inventory are.)
  - **Logs**: Explore → **Loki** → `{job="traefik/access"}`

Tail the structured access logs:
```bash
kubectl logs -n traefik -l app.kubernetes.io/name=traefik -f
```

---

## Verify the extra middleware

The brief asks for JWT + per-tier rate limiting **plus at least one additional middleware**.
Beyond those, every chain adds the **Headers** middleware (response hardening, all routes),
and the **enterprise** chain adds an **IP allowlist** (network policy, defense in depth).
Here's how to prove each is actually enforced — not just declared.

### Security headers (Headers middleware — all tiers)

```bash
TOKEN=$(./scripts/get-token.sh bob)
curl -sk -D - -o /dev/null -H "Authorization: Bearer $TOKEN" https://api.local/pro \
  | grep -iE 'strict-transport-security|content-security-policy|x-content-type-options|x-frame-options|referrer-policy|permissions-policy'
```
- *Expect* every one of these on the response:
  ```
  strict-transport-security: max-age=31536000; includeSubDomains; preload
  content-security-policy: default-src 'self'; frame-ancestors 'none'; object-src 'none'; base-uri 'self'
  x-content-type-options: nosniff
  x-frame-options: DENY
  referrer-policy: strict-origin-when-cross-origin
  permissions-policy: camera=(), microphone=(), geolocation=()
  ```
- *Why:* the gateway hardens responses uniformly. The audit answer to "where are our security
  headers enforced?" is one CRD, not N service codebases.

Confirm the backend's identity is **not** leaked (the middleware strips these):
```bash
curl -sk -D - -o /dev/null -H "Authorization: Bearer $TOKEN" https://api.local/pro \
  | grep -iE '^server:|^x-powered-by:' && echo "LEAK!" || echo "Server / X-Powered-By stripped ✔"
```

Negative check — the headers come from the **gateway**, not the app. whoami (the raw echo)
adds no security headers itself, yet the response still has them:
```bash
./scripts/call-api.sh whoami alice | grep -iE 'strict-transport-security|x-frame-options'
# present on the /whoami response too -> proves it's the gateway, not the service
```

### IP allowlist (enterprise tier only)

The `chain-enterprise` middleware puts `ip-allowlist-enterprise` **first**, so a disallowed
source is rejected before auth even runs. Prove enforcement by narrowing the allowed range so
your source no longer matches, then restoring it:

```bash
# Baseline — enterprise works for an allowed source
./scripts/call-api.sh enterprise carol            # HTTP/2 200

# Break it: allow only a range that does NOT include the kind ingress source
kubectl patch middleware ip-allowlist-enterprise -n apps --type merge \
  -p '{"spec":{"ipAllowList":{"sourceRange":["10.99.0.0/16"]}}}'

./scripts/call-api.sh enterprise carol            # HTTP/2 403  <- blocked by IP allowlist
./scripts/call-api.sh pro bob                      # HTTP/2 200  <- other tiers unaffected

# Restore
kubectl apply -f middlewares/04-ipallowlist.yaml
./scripts/call-api.sh enterprise carol            # HTTP/2 200 again
```
- *Why:* network policy is layered on JWT (defense in depth) and scoped to a **single route** —
  proof the chain is composed per-tier, not one-size-fits-all.
- *Note (kind):* behind a NodePort, Traefik sees the kind Docker-bridge IP as the source, not
  your laptop's. The allowed ranges in `middlewares/04-ipallowlist.yaml` cover that; if the
  baseline call is unexpectedly `403`, find your source with
  `kubectl logs -n traefik -l app.kubernetes.io/name=traefik | grep ClientHost | tail -1`
  and add that range. The break/restore demo works regardless.

### See the chain itself

```bash
kubectl get middlewares -n apps        # jwt-auth, ratelimit-*, security-headers, ip-allowlist-enterprise, strip-*, chain-*
kubectl describe middleware chain-enterprise -n apps   # shows the ordered list it composes
```

---

## Other middleware we could chain

This demo uses **jwt + rateLimit + headers + ipAllowList + stripPrefix**, composed with
**chain**. Traefik (OSS + Hub) ships many more — all configured the same declarative way, all
enforced centrally at the gateway. The ones most relevant to a zero-trust / financial-services
gateway:

| Middleware | OSS/Hub | What it does | How it'd extend this demo |
|---|---|---|---|
| `jwt` | Hub | Validate JWT against JWKS | **used** — edge authentication |
| `rateLimit` | OSS | Per-key token-bucket quota | **used** — per-tier, keyed on JWT user |
| `headers` | OSS | Request/response header policy | **used** — HSTS/CSP/nosniff hardening |
| `ipAllowList` | OSS | Allow/deny by source CIDR | **used** — enterprise network policy |
| `stripPrefix` | OSS | Rewrite path before backend | **used** — strip the tier prefix |
| `chain` | OSS | Compose middlewares in order | **used** — one ref per route |
| `oidc` | Hub | Interactive OIDC login (redirect to IdP, session cookie) | Protect a **browser-facing** app/dashboard with full Keycloak login, not just bearer tokens |
| `apiKey` | Hub | API-key auth (header/query/cookie) | Authenticate **machine/partner** clients that don't do OAuth |
| `oAuthIntrospection` | Hub | Validate **opaque** tokens via the IdP's introspection endpoint | Support reference tokens / instant revocation (JWT can't be revoked mid-life) |
| `forwardAuth` | OSS | Delegate the authz decision to an external service | Call an **OPA / policy engine** for fine-grained, attribute-based authorization |
| `passTLSClientCert` | OSS | Forward client-cert details as headers | **mTLS** client identity — common in fin-services B2B/partner APIs |
| `inFlightReq` | OSS | Cap concurrent in-flight requests | Protect a slow backend from concurrency overload (complements rate limiting) |
| `circuitBreaker` | OSS | Trip on backend error rate / latency | Shed load and fail fast when `orders`/`inventory` degrade |
| `retry` | OSS | Retry failed requests to healthy replicas | Smooth over transient backend errors |
| `buffering` | OSS | Limit request/response body size | Reject oversized payloads at the edge (DoS / abuse guard) |
| `errors` | OSS | Serve custom error pages from a service | Branded, non-leaky `401`/`403`/`429` responses |
| `compress` | OSS | gzip/brotli responses | Bandwidth/latency win for JSON-heavy APIs |
| `redirectScheme` | OSS | Force HTTP→HTTPS (or scheme/host) | Belt-and-suspenders alongside HSTS |
| `basicAuth` / `digestAuth` | OSS | Static credential auth | Quick protection for an internal/ops endpoint |

The pitch for the audience: **every** one of these is a Kubernetes CRD added to a route's chain —
authentication, authorization, hardening, traffic shaping, and resilience all live at the gateway
as versioned config, never in the services.

---

## Repository layout

```
.
├── kind/kind-config.yaml          kind cluster (host 80/443 -> NodePorts)
├── traefik/values.yaml            Traefik Hub Helm values (image, OTLP, logs, dashboard)
├── keycloak/
│   ├── keycloak.yaml              Keycloak deployment + service
│   └── realm-demo.json            "demo" realm: client, groups, users alice/bob/carol
├── apps/
│   ├── whoami.yaml                three whoami backends (free/pro/enterprise)
│   ├── instrumented.yaml          OTel-instrumented orders + inventory (distributed trace)
│   └── echo-svc/                  Go source + Dockerfile for the instrumented service
├── middlewares/
│   ├── 01-jwt-auth.yaml           JWT validation against Keycloak JWKS
│   ├── 02-ratelimits.yaml         three rate limiters (per-user via X-User-Id)
│   ├── 03-security-headers.yaml   HSTS / CSP / nosniff / frame-deny / referrer
│   ├── 04-ipallowlist.yaml        enterprise-only source range control
│   ├── 05-chains.yaml             stripPrefix + one chain middleware per tier
│   └── 06-traced-chain.yaml       chain + stripPrefix for the /orders route
├── routes/
│   └── ingressroutes.yaml         api.local tiers + /orders + keycloak.local + grafana.local
│                                  (Traefik dashboard is exposed by the Helm chart itself)
├── observability/
│   ├── lgtm.yaml                  grafana/otel-lgtm all-in-one (+ Traefik dashboard mount)
│   ├── alloy.yaml                 Alloy DaemonSet -> Loki
│   └── grafana/                   Traefik dashboard JSON + provisioning config
└── scripts/                       prepull / build-instrumented / token / call / load / teardown
```

---

## Troubleshooting

| Symptom | Fix |
|---|---|
| `401` on a valid token | Keycloak not ready yet, or JWKS unreachable. `kubectl get pods -n keycloak`; re-run the call after it's `Running`. |
| Helm install fails on `hub.token` | The `license` secret is missing or `TRAEFIK_HUB_TOKEN` was empty. Re-run step 4. |
| Browser can't resolve `*.local` | `/etc/hosts` entry missing (step 3). `*.local` is **not** auto-resolved on macOS. |
| Helm rejects a `tracing`/`metrics` key | Chart version skew. Check `helm show values traefik/traefik | grep -A2 otlp` and align `traefik/values.yaml`. |
| Enterprise route returns `403` unexpectedly | Your kind Docker subnet differs. `docker network inspect kind -f '{{(index .IPAM.Config 0).Subnet}}'` and update `middlewares/04-ipallowlist.yaml`. |
| Want a specific Hub image version | Set `image.tag` in `traefik/values.yaml` (e.g. `v3.11.0`) and update `scripts/images.txt` to match. |
| Intermittent `000` / Traefik `CrashLoopBackOff` with `failed to maintain leader elector` | Traefik Hub renews a Kubernetes **leader-election lease**; if it can't, it exits. Two causes seen locally: **(1)** Traefik CPU-throttled under load starves its heartbeat — already fixed by removing Traefik's CPU limit in `values.yaml`. **(2)** The kind control plane itself flaps: if `kube-scheduler`/`kube-controller-manager` show **many restarts** (and go `READY=false`), the laptop has been **sleeping** — clock jumps expire all leader-election leases at once (scheduler, controller-manager, *and* Traefik). Recover now with `kubectl delete pod -n kube-system kube-scheduler-* kube-controller-manager-* --force` (kubelet recreates them). **Prevent it:** start from a **fresh cluster** for the demo and run `caffeinate -dimsu &` so the Mac never sleeps. Keep `loadtest.sh` counts modest. |
| Rate limit doesn't trip / limits look doubled | `rateLimit` is per-Traefik-replica (in-memory). Keep `deployment.replicas: 1` (the default here) so the per-tier limits are exact. |

---

## Teardown
```bash
./scripts/99-teardown.sh     # deletes the cluster; image cache is preserved
```

See **[DEMO-SCRIPT.md](./DEMO-SCRIPT.md)** for the 15-minute presentation walkthrough.
