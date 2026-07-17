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

package v1

import (
	"github.com/opendatahub-io/opendatahub-operator/v2/api/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	FeastOperatorComponentName = "feastoperator"
	FeastOperatorInstanceName  = "default-feastoperator"
	FeastOperatorKind          = "FeastOperator"
)

// Compile-time interface assertion.
var _ common.PlatformObject = (*FeastOperator)(nil)

// FeastOperatorSpec defines the desired state of FeastOperator.
type FeastOperatorSpec struct {
	// OIDC holds the OIDC issuer settings. When the cluster uses external OIDC
	// the issuer URL is written to params.env before kustomize renders manifests.
	// +optional
	OIDC *common.GatewayOIDCSpec `json:"oidc,omitempty"`
}

// FeastOperatorStatus defines the observed state of FeastOperator.
type FeastOperatorStatus struct {
	common.Status                 `json:",inline"`
	common.ComponentReleaseStatus `json:",inline"`
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

func (c *FeastOperator) GetStatus() *common.Status {
	return &c.Status.Status
}

func (c *FeastOperator) GetConditions() []common.Condition {
	return c.Status.GetConditions()
}

func (c *FeastOperator) SetConditions(conditions []common.Condition) {
	c.Status.SetConditions(conditions)
}

func (c *FeastOperator) GetReleaseStatus() *[]common.ComponentRelease {
	return &c.Status.Releases
}

func (c *FeastOperator) SetReleaseStatus(releases []common.ComponentRelease) {
	c.Status.Releases = releases
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
