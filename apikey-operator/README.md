# API Key Operator

A small Kubernetes operator (kubebuilder / controller-runtime) that lets developers
**self-service opaque, tier-scoped, time-limited API keys** for the zero-trust gateway —
declaratively, with `kubectl apply`, and **without any Keycloak credentials**.

It complements the gateway's JWT flow: a human authenticates with their OAuth JWT, and
can additionally mint API keys for non-interactive clients (a CLI, a CI job, an AI agent).
Keys are first-class Kubernetes objects that persist for audit even after expiry/revocation.

> Lives on branch `feat/apikey-operator` only — `main` is unaffected.

## How it works

```
  kubectl apply APIKey ──▶ operator ──┬─▶ Secret <name>-apikey   (dev namespace)
   (spec.tier, expiresAt)             │     api-key: ak_…         ← the dev grabs this
                                      │     key-hash: $2y$…
                                      │
                                      └─▶ apps namespace (gateway):
                                            Secret  apikeys-<tier>   (bcrypt hashes)
                                            Middleware apikey-<tier> (Traefik apiKey,
                                              keySource header X-API-Key, secretValues→hashes)

  caller ──https + "X-API-Key: ak_…"──▶ Traefik route (HeaderRegexp match)
            ──▶ apikey-<tier> validates the bcrypt hash ──▶ rate limit ──▶ orders→inventory
```

For each reconcile the controller:
1. **Mints once** — generates `ak_<base32>`, writes it (and its bcrypt hash) to a Secret
   `<name>-apikey` in the APIKey's namespace, owner-referenced so it's GC'd on delete.
2. **Bridges to the gateway** — registers the hash in the per-tier aggregation Secret
   `apikeys-<tier>` (in `apps`) and rebuilds the `apikey-<tier>` Traefik middleware's
   `secretValues`. A `tier: "*"` key is registered in **all** tiers.
3. **Enforces lifecycle** — at `expiresAt` (requeued) or when `spec.revoked: true`, removes
   the hash so the gateway rejects it, and sets `status.phase` to `Expired`/`Revoked`. The
   APIKey object and its Secret **remain** (audit). A finalizer cleans the gateway hashes
   only on actual deletion.

## CRD

```yaml
apiVersion: gateway.zerotrust.io/v1alpha1
kind: APIKey
metadata: { name: acme-cli, namespace: apps }
spec:
  tier: pro                      # free | pro | enterprise | "*"   (required)
  expiresAt: 2026-12-31T23:59:59Z  # optional; omit = no expiry
  owner: acme-corp               # optional, self-asserted metadata (not a security control)
  description: "CLI client"      # optional
  revoked: false                 # flip to true to invalidate while keeping the object
```
`kubectl get ak -A` shows tier, phase, secret, and expiry via printer columns.

## Image: built in CI, published to Docker Hub (multi-arch)

The operator image is built by [`.github/workflows/apikey-operator-image.yml`](../.github/workflows/apikey-operator-image.yml)
and pushed **multi-arch (`linux/amd64` + `linux/arm64`)** to Docker Hub as
`docker.io/ams0/apikey-operator:demo` (plus branch + `sha` tags). The kind cluster pulls
it directly — no `kind load` needed.

- Requires repo secrets **`DOCKERHUB_USERNAME`** and **`DOCKERHUB_TOKEN`** (a Docker Hub
  access token). Push to the branch (or run the workflow manually) to build.
- Keep the Docker Hub repo **public** so the cluster pulls anonymously (no imagePullSecret).
  If your Docker Hub username isn't `ams0`, update the image in `config/manager/manager.yaml`.

## Deploy (kind cluster with the base demo running)

```bash
cd apikey-operator
make manifests generate          # (re)generate CRD/RBAC/deepcopy (only if you changed types)
make deploy                      # CRD + RBAC + manager (ns: apikey-operator-system)
make gateway                     # dual-auth X-API-Key routes on the gateway
```

**Local-dev alternative (no CI):** build + side-load a local image and point the manager at it:
```bash
make kind-load                   # builds apikey-operator:demo for your arch, loads into kind
#   then set image: apikey-operator:demo + imagePullPolicy: IfNotPresent in config/manager/manager.yaml
make deploy
```

## Use it

```bash
kubectl apply -f config/samples/gateway_v1alpha1_apikey.yaml
kubectl get ak -n apps
KEY=$(kubectl get secret acme-cli-apikey -n apps -o jsonpath='{.data.api-key}' | base64 -d)

curl -sk -H "X-API-Key: $KEY" https://api.local/pro      # 200 (orders→inventory)
curl -sk -H "X-API-Key: $KEY" https://api.local/free     # 200 only if tier is free or "*"
# revoke:
kubectl patch apikey acme-cli -n apps --type merge -p '{"spec":{"revoked":true}}'
curl -sk -H "X-API-Key: $KEY" https://api.local/pro      # 401 — gateway no longer knows the hash
```

A JWT caller (`Authorization: Bearer …`) on the same path still hits the existing JWT
chain — the API-key route only matches when the `X-API-Key` header is present.

## Design notes & v1alpha1 limitations

- **Provenance is deferred.** `spec.owner` is self-asserted metadata, *not* a security
  control. Tamper-proof creator binding (admission webhook reading `userInfo`) is fully
  designed in [docs/api-key-identity-provenance.md](docs/api-key-identity-provenance.md).
- **Key value is stored in the Secret** (so the dev can retrieve it), not show-once.
- **Per-tier middleware appears on first key.** `apikey-<tier>` is operator-created; until a
  tier has at least one key, its chain reference is unresolved (only affects the API-key
  path for that tier, never the JWT path).
- **Single reconcile worker** (`MaxConcurrentReconciles: 1`) so the shared aggregation
  Secrets are mutated without races.
- `GATEWAY_NAMESPACE` (env on the manager, default `apps`) controls where the aggregation
  Secrets/Middlewares are written — keep it equal to where the Traefik routes live.
