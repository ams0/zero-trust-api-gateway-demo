# API Key Identity & Provenance Assertion

> **Status:** Analysis captured — implementation **deferred** for `v1alpha1`.
> **Branch:** `feat/apikey-operator`
> **Decision:** the first operator release ships *without* the provenance webhook.
> `spec.owner` is self-asserted metadata only. This document records the design so
> the tamper-proof version can be added later without re-deriving it.

## Problem

We want every `APIKey` to be **bound to the authenticated principal that created it**
— a human dev or an automation identity — in a way the creator **cannot forge**, so
that "who issued this key?" is answerable forever (audit, compliance, revoke-by-user,
governance).

## Why the controller cannot do this on its own

The controller (controller-runtime reconcile loop) only ever sees the **stored object**.
By the time it reconciles, the identity of the requester is gone. Specifically:

- The authenticated principal (`username`, `groups`, `uid`) exists **only at admission
  time**, inside the API server's `AdmissionRequest.userInfo`. It is never persisted
  onto the object by default.
- `spec.owner` set by the user is **self-asserted** — anyone who can create an `APIKey`
  can write any value. Not trustworthy.
- `metadata.managedFields` records the **manager** (e.g. `kubectl`), not the user
  identity. Not usable for provenance.

**Conclusion:** the only place to capture the real creator is an **admission webhook**,
which runs server-side during the `CREATE` request and has access to `userInfo`.

## The correct mechanism: admission webhooks

### 1. Mutating webhook — capture the creator

On `CREATE`, read `req.UserInfo` and stamp **immutable** annotations the user did not
(and cannot) set themselves:

```
gateway.zerotrust.io/created-by:        <userInfo.Username>
gateway.zerotrust.io/created-by-groups: <comma-separated userInfo.Groups>
gateway.zerotrust.io/created-by-uid:    <userInfo.UID>
```

The controller then surfaces these in `status.createdBy` and copies them onto the issued
Secret's labels, so provenance is queryable from the key, the object, and the Secret.

Handler sketch (controller-runtime `admission.Handler`):

```go
func (w *APIKeyCreator) Handle(ctx context.Context, req admission.Request) admission.Response {
    var key gatewayv1alpha1.APIKey
    if err := w.decoder.Decode(req, &key); err != nil {
        return admission.Errored(http.StatusBadRequest, err)
    }
    // req.UserInfo is set by the API server from the authenticated request.
    if key.Annotations == nil { key.Annotations = map[string]string{} }
    key.Annotations[AnnCreatedBy]       = req.UserInfo.Username
    key.Annotations[AnnCreatedByGroups] = strings.Join(req.UserInfo.Groups, ",")
    key.Annotations[AnnCreatedByUID]    = req.UserInfo.UID

    marshaled, _ := json.Marshal(key)
    return admission.PatchResponseFromRaw(req.Object.Raw, marshaled)
}
```

This works for automation too: a CI job or agent using a ServiceAccount appears as
`system:serviceaccount:<ns>:<name>`, so you can always tell a human from a bot.

### 2. Validating webhook — immutability + (optional) governance

- **Immutability:** on `UPDATE`, reject any change to the `created-by*` annotations
  (compare `oldObject` vs `object`). Provenance, once stamped, is permanent.
- **Entitlement enforcement (optional, strong story):** reject `spec.tier: enterprise`
  unless the creator is in the `enterprise` group (or passes a `SubjectAccessReview`).
  A dev can then only mint keys **≤ their own level** — turning the gateway tiers into
  an actual authorization boundary on key issuance.

## Identity unification with OIDC (the narrative payoff)

If the cluster's API server uses **OIDC authentication backed by the same Keycloak**
that fronts the gateway, then `userInfo.Username` *is* the dev's real corporate identity.
The story becomes:

> The human authenticates **once** — to the cluster, via OIDC. Every opaque API key they
> mint is cryptographically bound to that identity for audit, even though the keys
> themselves are opaque and have **zero runtime dependency** on Keycloak.

Two identity planes, cleanly separated but provably linked:

| Plane | Who | How authenticated | Used for |
|---|---|---|---|
| Control plane | the dev / CI minting keys | kube-apiserver OIDC (Keycloak) | provenance on `APIKey` creation |
| Data plane | the CLI / agent calling the API | opaque `X-API-Key` (bcrypt-verified at the gateway) | request access, tier-scoped, time-limited |

## Cost / why it's deferred

- Webhooks must serve **TLS**, requiring a serving cert + `caBundle` injection into the
  webhook configuration — normally via **cert-manager** (kubebuilder scaffolds the
  manifests), or an operator-managed self-signed cert. That's a new cluster dependency.
- `failurePolicy` is a real tradeoff: `Fail` (a down webhook blocks all `APIKey` creation
  — safer for governance) vs `Ignore` (creation proceeds unstamped — safer for
  availability). Either choice needs operational thought.
- For the demo's scope, this complexity outweighs the benefit **right now**. The opaque-key
  mechanics, tier scoping, expiry, and revocation are the core; provenance hardening is an
  additive layer.

## Decision

`v1alpha1` ships **without** the webhook:

- `spec.owner` / `spec.description` are accepted as **self-asserted metadata** and echoed
  into `status` and the Secret labels. They are convenience/labeling, **not** a security
  control — documented as such.
- No cert-manager dependency, no webhook server.

## Future implementation checklist

When provenance becomes a requirement:

1. `kubebuilder create webhook --group gateway.zerotrust.io --version v1alpha1 --kind APIKey --defaulting --programmatic-validation`
2. Implement the mutating handler (capture `UserInfo` → `created-by*` annotations).
3. Implement the validating handler (immutability of `created-by*`; optional tier
   entitlement via group membership / `SubjectAccessReview`).
4. Add cert-manager (or self-signed cert management) + `caBundle` injection.
5. Choose `failurePolicy` deliberately and document the availability/governance tradeoff.
6. Surface `created-by` in `status.createdBy` and on the issued Secret's labels.
7. Update the demo script: "this key was provably issued by `alice@corp` — here's the
   audit trail," and show a denied `enterprise` key from a `pro`-group user.
