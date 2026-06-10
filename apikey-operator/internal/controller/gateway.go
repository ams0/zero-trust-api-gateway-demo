package controller

import (
	"context"
	"fmt"
	"os"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gatewayv1alpha1 "github.com/ams0/zero-trust-api-gateway-demo/apikey-operator/api/v1alpha1"
)

// apiKeyHeader is where the gateway reads the opaque key from (keySource.header).
const apiKeyHeader = "X-API-Key"

// concreteTiers are the real tiers an aggregation Secret/Middleware exists for.
// A key with tier "*" is registered into all of them.
var concreteTiers = []string{"free", "pro", "enterprise"}

// traefikMiddlewareGVK identifies the Traefik Middleware CRD we manage.
var traefikMiddlewareGVK = schema.GroupVersionKind{
	Group:   "traefik.io",
	Version: "v1alpha1",
	Kind:    "Middleware",
}

// gatewayNamespace is where the per-tier aggregation Secrets and apiKey
// Middlewares live (next to the Traefik routes). Defaults to "apps".
func gatewayNamespace() string {
	if ns := os.Getenv("GATEWAY_NAMESPACE"); ns != "" {
		return ns
	}
	return "apps"
}

func tierSecretName(tier string) string     { return "apikeys-" + tier }
func tierMiddlewareName(tier string) string { return "apikey-" + tier }

// entryKey is the per-key Secret data key inside an aggregation Secret. Namespace
// and name are RFC1123 labels, so "<ns>.<name>" is a valid Secret data key.
func entryKey(key *gatewayv1alpha1.APIKey) string {
	return key.Namespace + "." + key.Name
}

// targetTiers returns the concrete tiers a key applies to. "*" => all.
func targetTiers(tier gatewayv1alpha1.Tier) map[string]bool {
	out := map[string]bool{}
	if tier == "*" {
		for _, t := range concreteTiers {
			out[t] = true
		}
		return out
	}
	out[string(tier)] = true
	return out
}

// syncTier upserts (want=true) or removes (want=false) this key's hash entry in
// the given tier's aggregation Secret, then rebuilds that tier's apiKey Middleware
// secretValues from the Secret's current entries.
func (r *APIKeyReconciler) syncTier(ctx context.Context, tier, entry string, hash []byte, want bool) error {
	ns := gatewayNamespace()

	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: tierSecretName(tier), Namespace: ns}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, sec, func() error {
		sec.Type = corev1.SecretTypeOpaque
		if sec.Labels == nil {
			sec.Labels = map[string]string{}
		}
		sec.Labels["app.kubernetes.io/managed-by"] = "apikey-operator"
		sec.Labels["gateway.zerotrust.io/tier"] = tier
		if sec.Data == nil {
			sec.Data = map[string][]byte{}
		}
		if want {
			sec.Data[entry] = hash
		} else {
			delete(sec.Data, entry)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("aggregation secret %s/%s: %w", ns, tierSecretName(tier), err)
	}

	urns := make([]string, 0, len(sec.Data))
	for k := range sec.Data {
		urns = append(urns, fmt.Sprintf("urn:k8s:secret:%s:%s", tierSecretName(tier), k))
	}
	sort.Strings(urns)

	return r.ensureTierMiddleware(ctx, tier, urns)
}

// ensureTierMiddleware creates/updates the Traefik apiKey Middleware for a tier so
// its secretValues match the current set of registered key hashes.
func (r *APIKeyReconciler) ensureTierMiddleware(ctx context.Context, tier string, urns []string) error {
	ns := gatewayNamespace()
	mw := &unstructured.Unstructured{}
	mw.SetGroupVersionKind(traefikMiddlewareGVK)
	mw.SetNamespace(ns)
	mw.SetName(tierMiddlewareName(tier))

	vals := make([]interface{}, len(urns))
	for i, u := range urns {
		vals[i] = u
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, mw, func() error {
		labels := mw.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels["app.kubernetes.io/managed-by"] = "apikey-operator"
		labels["gateway.zerotrust.io/tier"] = tier
		mw.SetLabels(labels)

		if err := unstructured.SetNestedField(mw.Object, apiKeyHeader, "spec", "plugin", "apiKey", "keySource", "header"); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(mw.Object, true, "spec", "plugin", "apiKey", "secretNonBase64Encoded"); err != nil {
			return err
		}
		return unstructured.SetNestedSlice(mw.Object, vals, "spec", "plugin", "apiKey", "secretValues")
	})
	if err != nil {
		return fmt.Errorf("apiKey middleware %s/%s: %w", ns, tierMiddlewareName(tier), err)
	}
	return nil
}

// removeFromAllTiers drops this key's entry from every tier (used on revoke/expiry/delete).
func (r *APIKeyReconciler) removeFromAllTiers(ctx context.Context, entry string) error {
	for _, tier := range concreteTiers {
		if err := r.syncTier(ctx, tier, entry, nil, false); err != nil {
			return err
		}
	}
	return nil
}
