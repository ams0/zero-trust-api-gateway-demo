package controller

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"golang.org/x/crypto/bcrypt"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	gatewayv1alpha1 "github.com/ams0/zero-trust-api-gateway-demo/apikey-operator/api/v1alpha1"
)

const (
	finalizer     = "gateway.zerotrust.io/finalizer"
	secretSuffix  = "-apikey"
	dataKeyValue  = "api-key"
	dataKeyHash   = "key-hash"
	dataKeyPrefix = "key-prefix"
)

// APIKeyReconciler reconciles APIKey objects.
type APIKeyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=gateway.zerotrust.io,resources=apikeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.zerotrust.io,resources=apikeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gateway.zerotrust.io,resources=apikeys/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=traefik.io,resources=middlewares,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile drives an APIKey toward its desired state: mint a key Secret, register
// (or remove) its hash with the gateway's per-tier apiKey middleware, and reflect
// the lifecycle phase in status.
func (r *APIKeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var key gatewayv1alpha1.APIKey
	if err := r.Get(ctx, req.NamespacedName, &key); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	entry := entryKey(&key)

	// Deletion: clean up gateway hashes, then drop the finalizer. The per-key
	// Secret is owner-referenced and garbage-collected with the object.
	if !key.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&key, finalizer) {
			if err := r.removeFromAllTiers(ctx, entry); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&key, finalizer)
			if err := r.Update(ctx, &key); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if controllerutil.AddFinalizer(&key, finalizer) {
		if err := r.Update(ctx, &key); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	hash, prefix, issuedAt, err := r.ensureKeySecret(ctx, &key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure key secret: %w", err)
	}

	now := time.Now()
	expired := key.Spec.ExpiresAt != nil && !key.Spec.ExpiresAt.Time.After(now)
	active := !key.Spec.Revoked && !expired
	targets := targetTiers(key.Spec.Tier)

	for _, tier := range concreteTiers {
		want := active && targets[tier]
		if err := r.syncTier(ctx, tier, entry, hash, want); err != nil {
			return ctrl.Result{}, err
		}
	}

	phase := gatewayv1alpha1.PhaseActive
	switch {
	case key.Spec.Revoked:
		phase = gatewayv1alpha1.PhaseRevoked
	case expired:
		phase = gatewayv1alpha1.PhaseExpired
	}

	key.Status.Phase = phase
	key.Status.KeyPrefix = prefix
	key.Status.SecretName = key.Name + secretSuffix
	key.Status.IssuedAt = issuedAt
	key.Status.ExpiresAt = key.Spec.ExpiresAt
	key.Status.ObservedGeneration = key.Generation
	setReadyCondition(&key, active, phase)
	if err := r.Status().Update(ctx, &key); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("reconciled", "phase", phase, "tier", key.Spec.Tier, "active", active)

	// Requeue so expiry is enforced promptly even with no spec change.
	if active && key.Spec.ExpiresAt != nil {
		d := time.Until(key.Spec.ExpiresAt.Time)
		if d < time.Second {
			d = time.Second
		}
		return ctrl.Result{RequeueAfter: d}, nil
	}
	return ctrl.Result{}, nil
}

// ensureKeySecret creates the per-key Secret (with a freshly minted key + bcrypt
// hash) if absent, or returns the existing hash/prefix. Returns the bcrypt hash.
func (r *APIKeyReconciler) ensureKeySecret(ctx context.Context, key *gatewayv1alpha1.APIKey) ([]byte, string, *metav1.Time, error) {
	name := key.Name + secretSuffix
	sec := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Namespace: key.Namespace, Name: name}, sec)

	switch {
	case err == nil:
		issued := &metav1.Time{Time: sec.CreationTimestamp.Time}
		changed := applyKeySecretMeta(sec, key)
		hash := sec.Data[dataKeyHash]
		if len(hash) == 0 {
			plain := sec.Data[dataKeyValue]
			if len(plain) == 0 {
				return nil, "", nil, fmt.Errorf("secret %s/%s has no key value", key.Namespace, name)
			}
			h, hErr := bcrypt.GenerateFromPassword(plain, bcrypt.DefaultCost)
			if hErr != nil {
				return nil, "", nil, hErr
			}
			sec.Data[dataKeyHash] = h
			hash = h
			changed = true
		}
		if changed {
			if uErr := r.Update(ctx, sec); uErr != nil {
				return nil, "", nil, uErr
			}
		}
		return hash, string(sec.Data[dataKeyPrefix]), issued, nil

	case apierrors.IsNotFound(err):
		plain, prefix, gErr := generateKey()
		if gErr != nil {
			return nil, "", nil, gErr
		}
		hash, hErr := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
		if hErr != nil {
			return nil, "", nil, hErr
		}
		newSec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: key.Namespace},
			Type:       corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				dataKeyValue:  []byte(plain),
				dataKeyHash:   hash,
				dataKeyPrefix: []byte(prefix),
			},
		}
		applyKeySecretMeta(newSec, key)
		if rErr := controllerutil.SetControllerReference(key, newSec, r.Scheme); rErr != nil {
			return nil, "", nil, rErr
		}
		if cErr := r.Create(ctx, newSec); cErr != nil {
			return nil, "", nil, cErr
		}
		return hash, prefix, &metav1.Time{Time: time.Now()}, nil

	default:
		return nil, "", nil, err
	}
}

// applyKeySecretMeta sets labels/annotations/informational data on the per-key
// Secret and reports whether anything changed (to avoid update loops).
func applyKeySecretMeta(sec *corev1.Secret, key *gatewayv1alpha1.APIKey) bool {
	changed := false
	setMap := func(m *map[string]string, k, v string) {
		if *m == nil {
			*m = map[string]string{}
		}
		if (*m)[k] != v {
			(*m)[k] = v
			changed = true
		}
	}
	setMap(&sec.Labels, "app.kubernetes.io/managed-by", "apikey-operator")
	setMap(&sec.Labels, "gateway.zerotrust.io/tier", string(key.Spec.Tier))
	setMap(&sec.Annotations, "gateway.zerotrust.io/owner", key.Spec.Owner)
	setMap(&sec.Annotations, "gateway.zerotrust.io/description", key.Spec.Description)

	if sec.Data == nil {
		sec.Data = map[string][]byte{}
	}
	setData := func(k, v string) {
		if string(sec.Data[k]) != v {
			sec.Data[k] = []byte(v)
			changed = true
		}
	}
	setData("tier", string(key.Spec.Tier))
	exp := ""
	if key.Spec.ExpiresAt != nil {
		exp = key.Spec.ExpiresAt.Time.UTC().Format(time.RFC3339)
	}
	setData("expires-at", exp)
	return changed
}

func setReadyCondition(key *gatewayv1alpha1.APIKey, active bool, phase gatewayv1alpha1.APIKeyPhase) {
	cond := metav1.Condition{
		Type:               "Ready",
		ObservedGeneration: key.Generation,
	}
	if active {
		cond.Status = metav1.ConditionTrue
		cond.Reason = "Active"
		cond.Message = "API key is active and accepted by the gateway"
	} else {
		cond.Status = metav1.ConditionFalse
		cond.Reason = string(phase)
		cond.Message = "API key is " + strings.ToLower(string(phase))
	}
	apimeta.SetStatusCondition(&key.Status.Conditions, cond)
}

// generateKey returns a random opaque key ("ak_<base32>") and a short identifying prefix.
func generateKey() (full string, prefix string, err error) {
	buf := make([]byte, 30)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	enc := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf))
	full = "ak_" + enc
	p := enc
	if len(p) > 8 {
		p = p[:8]
	}
	return full, "ak_" + p, nil
}

// SetupWithManager registers the controller. MaxConcurrentReconciles is 1 so the
// shared per-tier aggregation Secrets/Middlewares are mutated without races.
func (r *APIKeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1alpha1.APIKey{}).
		Owns(&corev1.Secret{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Named("apikey").
		Complete(r)
}
