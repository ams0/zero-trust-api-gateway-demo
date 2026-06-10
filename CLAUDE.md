# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

A **demo**, not a deployable product: it stands up Traefik Hub as a zero-trust API gateway on a local **kind** cluster, in front of backend microservices, to show centralized JWT auth + per-tier rate limiting + security hardening + full observability. The deliverables are Kubernetes manifests, Helm values, a deployment runbook (`README.md`), and a 15-minute presentation walkthrough (`DEMO-SCRIPT.md`). Optimize changes for *demo clarity and reliability*, not production hardening.

There are **no automated tests**. "Verification" means running the runbook and the helper scripts and observing behavior (see `README.md` → *Verify it works* / *Verify the extra middleware*).

## Commands

Deployment is **manual and step-by-step** (the README is the source of truth; there is intentionally no one-shot install script). Cluster name is `zero-trust-gw`; the Hub license is supplied via `$TRAEFIK_HUB_TOKEN`.

```bash
# images: pre-pull (slow, do early), then create cluster + side-load
./scripts/00-prepull-images.sh
kind create cluster --config kind/kind-config.yaml
# load images via single-platform archive (see "kind image loading" below)

# the only buildable code — the instrumented Go service:
./scripts/build-instrumented.sh          # docker build + load echo-svc:demo into the cluster
cd apps/echo-svc && go build ./... && go vet ./...   # compile/vet locally (Go 1.25+)

# install Traefik Hub (after the license secret exists in ns traefik):
helm upgrade --install traefik traefik/traefik -n traefik --values traefik/values.yaml --wait

# live-demo helpers (need /etc/hosts: 127.0.0.1 api.local keycloak.local grafana.local traefik.local):
./scripts/get-token.sh <alice|bob|carol>           # password-grant JWT from Keycloak
./scripts/call-api.sh <free|pro|enterprise|whoami> <user>   # single request, prints endpoint+auth+headers+body
./scripts/loadtest.sh <route> <user> [count] [conc]         # concurrent load to trip rate limits
./scripts/99-teardown.sh                            # delete the cluster
```

`go.sum` is committed; the Docker build uses `go mod download` for a reproducible, pinned build (do **not** revert it to `go mod tidy`).

## Architecture

**Everything is policy-at-the-edge.** Backends contain zero auth/rate-limit/header code; all of it is declared as Traefik `Middleware` CRDs and composed per-route.

- **Gateway** (`traefik/values.yaml`, ns `traefik`): Traefik **Hub** image (`ghcr.io/traefik/traefik-hub`, license secret named `license`). Exposed via NodePort, mapped to host 80/443 by `kind/kind-config.yaml`. Emits OTLP metrics+traces; JSON access logs.
- **Identity** (`keycloak/`, ns `keycloak`): Keycloak dev-mode, realm auto-imported from `realm-demo.json` (users alice/bob/carol in groups free/pro/enterprise). Traefik validates JWTs against Keycloak's in-cluster JWKS; clients fetch tokens via `https://keycloak.local`.
- **Backends** (`apps/`, ns `apps`): the three tiers (`/free`,`/pro`,`/enterprise`) all route to the **same instrumented API** `orders → inventory` (`apps/echo-svc`, image `echo-svc:demo`) — so every tier call produces a real distributed trace. `whoami` is a **separate `/whoami` inspection endpoint** (not a tier) that echoes raw request headers.
- **Observability** (`observability/`, ns `observability`): `grafana/otel-lgtm` all-in-one (OTel Collector + Prometheus + Tempo + Loki + Grafana) receives OTLP on :4317; Grafana Alloy ships Traefik access logs to Loki; a Traefik dashboard is provisioned via ConfigMap mounted into the LGTM pod.

**Middleware chains** (`middlewares/`, applied in array order): each route references one `chain-*` middleware. Order matters — `ip-allowlist` (enterprise) → `jwt-auth` → `security-headers` → `ratelimit-*` → `strip-*`. The rate limiter keys on `X-User-Id`, which `jwt-auth` injects from the validated `sub` claim, so it must run **after** jwt. The "additional middleware" the brief asks for is **`security-headers`** (HSTS/CSP/nosniff/frame-deny); the enterprise tier adds **`ipAllowList`** as a second extra.

**Routing model:** one `IngressRoute` per route (free/pro/enterprise/whoami), each named so Traefik's router/service metric labels read `apps-<name>-<hash>` (legible per-tier panels). Do **not** collapse them back into one multi-route object — it makes all metrics share one opaque hash.

## Critical constraints (these have bitten before)

- **Keep `deployment.replicas: 1`** in `traefik/values.yaml`. Traefik's `rateLimit` is an in-memory, per-replica token bucket; >1 replica makes per-tier limits ~Nx and split, breaking the rate-limit demo.
- **No CPU limit on Traefik** (memory limit only). A CPU limit throttles Traefik under load and starves its Hub leader-election heartbeat → lease renewal fails → it exits/crash-loops.
- **kind image loading** on Docker Desktop's containerd image store: `kind load docker-image` fails with `content digest … not found`. Use `docker image save --platform linux/<arch> <img> -o /tmp/x.tar && kind load image-archive /tmp/x.tar --name zero-trust-gw` (this is what `build-instrumented.sh` does). The host arch is `linux/$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')`.
- **Control-plane stability:** if the Mac sleeps, clock jumps expire all leader-election leases at once — `kube-scheduler`/`kube-controller-manager`/Traefik crash together (etcd stays healthy). Run `caffeinate -dimsu &` and prefer a fresh cluster for a real demo.
- **`echo-svc:demo` is built locally, never pulled** — it's not in `scripts/images.txt`. After any cluster recreate, re-run `./scripts/build-instrumented.sh`.
- **All credentials are throwaway demo values** (Keycloak admin/admin, client secret `gateway-demo-secret`, user password `password`, anonymous Grafana). The only real secret is `$TRAEFIK_HUB_TOKEN`, injected at runtime — never commit it.
- **Helm-chart schema is strict:** unknown keys fail `helm install`. Verify keys against the chart (`helm show values traefik/traefik`) before adding — e.g. websecure TLS is `ports.websecure.http.tls`, not `ports.websecure.tls`.

## Conventions

- Middlewares are numbered by concern (`01-jwt-auth` … `06-whoami-chain`); chains live alongside the primitives they compose.
- Keep auth terminating at the edge: `jwt-auth` sets `forwardAuthorization: false` so the backend never sees the token, only injected `X-User-*` headers.
- When adding telemetry deps to `apps/echo-svc`, keep `otel` / `otel/sdk` / `otlptracegrpc` on the same version and `contrib/otelhttp` on its matching `0.x` (otel `1.N` ↔ otelhttp `0.(N+25)`); bump `go.sum` and the Dockerfile `golang:` base together.
