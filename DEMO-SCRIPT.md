# 15-Minute Demo Walkthrough — Zero-Trust API Gateway

Audience: platform engineering at a financial services company. They care about
**security posture**, **operational simplicity**, and **reducing developer burden**.
The through-line: *every security decision moves to the gateway; the services stay dumb.*

> Pre-flight (do before they're in the room): README deploy steps completed, `/etc/hosts` set,
> these tabs open — terminal, `https://traefik.local/dashboard/`, `https://grafana.local`.
> Run `./scripts/call-api.sh free alice` once to warm caches and accept the self-signed cert.

---

## 0:00 – 1:30 · Frame the problem

Say:
> "In a typical microservices estate, every service re-implements auth, rate limiting,
> and security headers. That's N copies of security-critical code, N chances to get it
> wrong, and N teams you have to trust to patch it. For a regulated business that's both
> an audit nightmare and a developer tax. The zero-trust answer is to enforce policy in
> one place — the gateway — and let services do nothing but business logic."

Show: the architecture diagram in `README.md`. Point at the chain in the middle.

---

## 1:30 – 3:00 · The backend has no idea any of this exists

Say: "Here's our 'microservice'. It's `whoami` — it just echoes the request. No auth
code, no rate limiter, no header logic. Remember that."

```bash
kubectl get deploy -n apps          # orders, inventory, whoami — none contain auth code
```

Show the Traefik dashboard (`https://traefik.local/dashboard/`): the routers, the
middleware chains. "This is the entire security surface, declared as Kubernetes
resources — not scattered across service codebases."

---

## 3:00 – 6:00 · Authentication at the edge

**1. No token → rejected before it ever reaches a service.**
```bash
./scripts/call-api.sh free --anon          # HTTP/2 401
```
Say: "The request died at the gateway. The backend was never invoked."

**2. Valid token → allowed, and the gateway hands the service a trusted identity.**
```bash
./scripts/call-api.sh whoami alice         # the inspection endpoint echoes every header
```
Point at the echoed request headers — `X-User-Id`, `X-User-Name`, `X-User-Groups` are
present, and there is **no** `Authorization` header:
> "Traefik validated the JWT signature against Keycloak's JWKS, then projected the
> verified claims into headers. The service gets *identity it can trust* without ever
> parsing a token or knowing what Keycloak is. The `Authorization` header is stripped —
> the credential never leaves the edge."

(A real tier call returns the API itself: `./scripts/call-api.sh pro bob` → JSON from
`orders` with its `inventory` downstream, carrying the same injected `user`/`groups`.)

Show the middleware (`middlewares/01-jwt-auth.yaml`): "~10 lines. This is the only
auth code in the entire system."

---

## 6:00 – 9:00 · Per-tier rate limiting — keyed on the authenticated user

Say: "Three customer tiers, three quotas. And because the limiter keys on the JWT's
user id, it's *per-authenticated-user*, not per-IP — so one noisy tenant can't starve
another behind the same NAT."

```bash
./scripts/loadtest.sh free alice           # bursts, then 429s appear
./scripts/loadtest.sh pro  bob  40 20      # same load, all 200 — higher tier absorbs it
```
Show `middlewares/02-ratelimits.yaml` and `05-chains.yaml`:
> "Three `Middleware` CRDs with different limit/burst, composed per route with the
> `chain` middleware. Changing a customer's tier is a one-line route edit — no service
> redeploy, no code change."

---

## 9:00 – 11:00 · Hardening + network policy (defense in depth)

**Security headers on every tier:**
```bash
./scripts/call-api.sh pro bob | grep -iE 'strict-transport|content-security|x-content-type|x-frame'
```
Say: "HSTS, CSP, nosniff, frame-deny — applied uniformly at the edge. The audit answer
to 'where are our security headers enforced?' is one file, not forty repos."

**Enterprise-only IP allowlist — show enforcement live:**
```bash
# break it: allow a range that won't match, then watch enterprise 403 while free/pro keep working
kubectl patch middleware ip-allowlist-enterprise -n apps --type merge \
  -p '{"spec":{"ipAllowList":{"sourceRange":["10.99.0.0/16"]}}}'
./scripts/call-api.sh enterprise carol      # 403 Forbidden
./scripts/call-api.sh pro bob               # still 200
# restore
kubectl apply -f middlewares/04-ipallowlist.yaml
```
Say: "Network controls layered *on top of* JWT auth — defense in depth, per route,
changed in seconds."

---

## 11:00 – 13:30 · Observability — one pane for metrics, traces, logs

> Prep: just before this section, run `./scripts/loadtest.sh free alice` and a few
> `./scripts/call-api.sh pro bob` so the panels and traces have data.

Open Grafana (`https://grafana.local`). Walk three things:

1. **Metrics** — Dashboards → **Traefik — Zero-Trust Gateway**. The "Requests by status
   code" panel tells the whole story in one view: green 200s, the 401s from anonymous
   calls, and the 429s from the rate-limit demo. Also request rate by tier (free/pro/
   enterprise) and latency p50/p95/p99. "This dashboard is provisioned with the stack —
   every install has it."
2. **Traces** — Explore → **Tempo** → Search → run query → open a tier trace (e.g. a
   `/pro` call). Show the waterfall: `traefik (GET → ReverseProxy) → orders (handle →
   HTTP GET) → inventory (handle)`. "One trace ID, three services, end to end. The
   gateway starts the trace and propagates W3C context; the services continue it. Every
   tier call is traced because the tiers route to this instrumented API."
   - Aside: "Hit `/whoami` instead and you'd see a single Traefik edge span — that backend
     isn't instrumented. Traefik still hands it a trace ID; it just doesn't add spans.
     Instrument the service and its spans slot right in, like orders/inventory."
3. **Logs** — Explore → **Loki** → `{job="traefik/access"}`. Show a structured JSON
   access log line carrying the `X-User-Id` the gateway injected. "Auth, quota, and audit
   data correlated — and we never touched the service."

Say: "Metrics and traces are Traefik's native OTLP export; logs are one shipper. The
gateway gives you golden-signal observability for every service for free — and where a
team *does* instrument, it stitches into the same trace."

---

## 13:30 – 15:00 · Close — tie it to what they care about

> - **Security posture:** one enforcement point, declarative, auditable. Auth, rate
>   limiting, headers, network policy — all versioned Kubernetes resources, not code
>   spread across services. Credentials terminate at the edge.
> - **Operational simplicity:** Helm + a handful of CRDs. Change a tier or tighten a
>   policy with a one-line edit and `kubectl apply` — no service redeploys.
> - **Reduced developer burden:** service teams ship business logic. Zero auth code,
>   zero rate-limit code, zero header code. Security is the platform's job, centralized.

Optional one-liners if asked:
- *"Can it do per-route authZ, not just authN?"* → yes: the JWT middleware's `claims`
  field, e.g. `Equals(\`groups\`, \`enterprise\`)`. Show `middlewares/01-jwt-auth.yaml`.
- *"What about API keys / OAuth client-credentials / OIDC login?"* → Hub ships those as
  sibling middlewares; same chain pattern.

---

## Reset between runs
```bash
kubectl apply -f middlewares/04-ipallowlist.yaml   # if you broke the allowlist
# rate-limit buckets drain on their own in a second or two
```
