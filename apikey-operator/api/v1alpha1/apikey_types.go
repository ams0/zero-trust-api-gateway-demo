package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Tier is the access tier an API key is scoped to. "*" grants access to all tiers.
// +kubebuilder:validation:Enum=free;pro;enterprise;"*"
type Tier string

// APIKeyPhase is the lifecycle phase of an API key.
type APIKeyPhase string

const (
	// PhasePending means the key has not been fully provisioned yet.
	PhasePending APIKeyPhase = "Pending"
	// PhaseActive means the key is valid and accepted by the gateway.
	PhaseActive APIKeyPhase = "Active"
	// PhaseExpired means the key passed its expiresAt and is no longer accepted.
	PhaseExpired APIKeyPhase = "Expired"
	// PhaseRevoked means the key was explicitly revoked and is no longer accepted.
	PhaseRevoked APIKeyPhase = "Revoked"
)

// APIKeySpec defines the desired state of an APIKey.
type APIKeySpec struct {
	// Tier the key is scoped to: free, pro, enterprise, or "*" for all tiers.
	// +kubebuilder:validation:Required
	Tier Tier `json:"tier"`

	// ExpiresAt is an optional RFC3339 time after which the key is rejected.
	// Omit for a non-expiring key.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// Owner is free-form, self-asserted metadata identifying the consumer
	// (team, app, agent). NOT a security control — see docs/api-key-identity-provenance.md.
	// +optional
	Owner string `json:"owner,omitempty"`

	// Description is free-form human context for the key.
	// +optional
	Description string `json:"description,omitempty"`

	// Revoked, when true, immediately invalidates the key at the gateway while
	// keeping the APIKey object and its Secret for audit.
	// +optional
	// +kubebuilder:default=false
	Revoked bool `json:"revoked,omitempty"`
}

// APIKeyStatus defines the observed state of an APIKey.
type APIKeyStatus struct {
	// Phase is the lifecycle phase: Pending, Active, Expired, or Revoked.
	// +optional
	Phase APIKeyPhase `json:"phase,omitempty"`

	// KeyPrefix is the first few characters of the generated key, for identification.
	// The full key is only ever written to the Secret, never to status.
	// +optional
	KeyPrefix string `json:"keyPrefix,omitempty"`

	// SecretName is the Secret (in the APIKey's namespace) holding the key value.
	// +optional
	SecretName string `json:"secretName,omitempty"`

	// IssuedAt is when the key value was first generated.
	// +optional
	IssuedAt *metav1.Time `json:"issuedAt,omitempty"`

	// ExpiresAt mirrors spec.expiresAt for at-a-glance visibility.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// ObservedGeneration is the spec generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest observations of the key's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ak
// +kubebuilder:printcolumn:name="Tier",type=string,JSONPath=`.spec.tier`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Secret",type=string,JSONPath=`.status.secretName`
// +kubebuilder:printcolumn:name="Expires",type=string,JSONPath=`.status.expiresAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// APIKey is an opaque, tier-scoped, optionally time-limited API key issued for the
// gateway. The controller mints a key value into a Secret and registers its bcrypt
// hash with the gateway's per-tier Traefik apiKey middleware.
type APIKey struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   APIKeySpec   `json:"spec,omitempty"`
	Status APIKeyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// APIKeyList contains a list of APIKey.
type APIKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []APIKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&APIKey{}, &APIKeyList{})
}
