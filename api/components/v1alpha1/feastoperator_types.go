/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fwapi "github.com/opendatahub-io/odh-platform-utilities/framework/api"
)

const (
	FeastOperatorComponentName = "feastoperator"
	FeastOperatorInstanceName  = "default-feastoperator"
	FeastOperatorKind          = "FeastOperator"
)

// Compile-time interface assertions.
var _ fwapi.PlatformObject = (*FeastOperator)(nil)
var _ fwapi.WithReleases = (*FeastOperator)(nil)

// GatewayOIDCSpec is the minimal OIDC projection Feast needs for manifest
// rendering. It is defined locally because the framework does not provide
// an equivalent type (same shape and markers as the former
// opendatahub-operator api/common type).
type GatewayOIDCSpec struct {
	// IssuerURL is the OIDC issuer URL (for example "https://keycloak.example.com/realms/myorg").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Format=uri
	// +kubebuilder:validation:Pattern=`^https://\S+$`
	IssuerURL string `json:"issuerURL"`
}

// FeastOperatorSpec defines the desired state of FeastOperator.
type FeastOperatorSpec struct {
	// OIDC holds the OIDC issuer settings. When the cluster uses external OIDC
	// the issuer URL is written to params.env before kustomize renders manifests.
	// +optional
	OIDC *GatewayOIDCSpec `json:"oidc,omitempty"`

	// Capabilities declares the desired state for Feature Store and Data Registry.
	// When present, these values are authoritative and override process-level
	// environment defaults. When absent, the module falls back to startup
	// configuration (env vars) for backward compatibility.
	// +optional
	Capabilities *CapabilitiesSpec `json:"capabilities,omitempty"`
}

// CapabilitiesSpec declares per-capability management state. Both fields are
// required when the block is present — the handler must write the full block
// or omit it entirely.
// +kubebuilder:object:generate=true
type CapabilitiesSpec struct {
	// FeatureStore controls standard FeatureStore workload reconciliation.
	FeatureStore CapabilitySpec `json:"featureStore"`

	// DataRegistry controls Data Registry workload reconciliation and
	// namespace provisioning. The namespace is always rhoai-data-registry.
	DataRegistry CapabilitySpec `json:"dataRegistry"`
}

// CapabilityManagementState restricts the allowed management states for capability
// toggles to Managed or Removed. Unlike the platform's general ManagementState,
// Unmanaged and Force are not valid for capability toggles.
// +kubebuilder:validation:Enum=Managed;Removed
type CapabilityManagementState string

const (
	CapabilityManaged CapabilityManagementState = "Managed"
	CapabilityRemoved CapabilityManagementState = "Removed"
)

// CapabilitySpec holds the management state for a single capability.
// +kubebuilder:object:generate=true
type CapabilitySpec struct {
	ManagementState CapabilityManagementState `json:"managementState"`
}

// FeastOperatorStatus defines the observed state of FeastOperator.
type FeastOperatorStatus struct {
	fwapi.Status                 `json:",inline"`
	fwapi.ComponentReleaseStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'default-feastoperator'",message="FeastOperator name must be default-feastoperator"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`,description="Ready"
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`,description="Reason"

// FeastOperator is the Schema for the feastoperators API.
type FeastOperator struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FeastOperatorSpec   `json:"spec,omitempty"`
	Status FeastOperatorStatus `json:"status,omitempty"`
}

func (c *FeastOperator) GetStatus() *fwapi.Status {
	return &c.Status.Status
}

func (c *FeastOperator) GetConditions() []fwapi.Condition {
	return c.Status.GetConditions()
}

func (c *FeastOperator) SetConditions(conditions []fwapi.Condition) {
	c.Status.SetConditions(conditions)
}

func (c *FeastOperator) GetReleaseStatus() *fwapi.ComponentReleaseStatus {
	return &c.Status.ComponentReleaseStatus
}

func (c *FeastOperator) SetReleaseStatus(status fwapi.ComponentReleaseStatus) {
	c.Status.ComponentReleaseStatus = status
}

// +kubebuilder:object:root=true

// FeastOperatorList contains a list of FeastOperator.
type FeastOperatorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FeastOperator `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FeastOperator{}, &FeastOperatorList{})
}
