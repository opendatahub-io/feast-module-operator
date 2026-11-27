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
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/yaml"
)

func TestDataConnectHubRequiresExplicitEnablement(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec string
		want bool
	}{
		{"legacy environment defaults", `{}`, false},
		{"legacy capabilities", `{"capabilities":{"featureStore":{"managementState":"Managed"},"dataRegistry":{"managementState":"Managed"}}}`, false},
		{"DCH removed", `{"capabilities":{"featureStore":{"managementState":"Managed"},"dataRegistry":{"managementState":"Managed"},"dataConnectHub":{"managementState":"Removed"}}}`, false},
		{"DCH only", `{"capabilities":{"featureStore":{"managementState":"Removed"},"dataRegistry":{"managementState":"Removed"},"dataConnectHub":{"managementState":"Managed"}}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var spec FeastOperatorSpec
			require.NoError(t, json.Unmarshal([]byte(tc.spec), &spec))
			require.Equal(t, tc.want, spec.DataConnectHubEnabled())
		})
	}
}

func TestDataConnectHubCapabilityDeepCopy(t *testing.T) {
	obj := &FeastOperator{Spec: FeastOperatorSpec{Capabilities: &CapabilitiesSpec{
		DataConnectHub: &DataConnectHubCapabilitySpec{
			CapabilitySpec:       CapabilitySpec{ManagementState: CapabilityManaged},
			Gateway:              &DataConnectHubGatewaySpec{Name: "gateway", Namespace: "ingress"},
			TokenReviewAudiences: []string{"https://issuer.example"},
		},
	}}}
	copy := obj.DeepCopy()
	copy.Spec.Capabilities.DataConnectHub.Gateway.Name = "other"
	copy.Spec.Capabilities.DataConnectHub.TokenReviewAudiences[0] = "https://other.example"
	require.Equal(t, "gateway", obj.Spec.Capabilities.DataConnectHub.Gateway.Name)
	require.Equal(t, []string{"https://issuer.example"}, obj.Spec.Capabilities.DataConnectHub.TokenReviewAudiences)
}

func TestDataConnectHubEnablementIsIndependent(t *testing.T) {
	states := []CapabilityManagementState{CapabilityManaged, CapabilityRemoved}
	for _, featureStore := range states {
		for _, dataRegistry := range states {
			for _, dataConnectHub := range states {
				name := fmt.Sprintf("featureStore=%s/dataRegistry=%s/dataConnectHub=%s", featureStore, dataRegistry, dataConnectHub)
				t.Run(name, func(t *testing.T) {
					spec := FeastOperatorSpec{Capabilities: &CapabilitiesSpec{
						FeatureStore: CapabilitySpec{ManagementState: featureStore},
						DataRegistry: CapabilitySpec{ManagementState: dataRegistry},
						DataConnectHub: &DataConnectHubCapabilitySpec{
							CapabilitySpec: CapabilitySpec{ManagementState: dataConnectHub},
						},
					}}
					require.Equal(t, dataConnectHub == CapabilityManaged, spec.DataConnectHubEnabled())
				})
			}
		}
	}
}

func TestDataConnectHubCapabilitySchema(t *testing.T) {
	data, err := os.ReadFile("../../../config/crd/bases/components.platform.opendatahub.io_feastoperators.yaml")
	require.NoError(t, err)
	var crd apiextensionsv1.CustomResourceDefinition
	require.NoError(t, yaml.Unmarshal(data, &crd))
	require.Len(t, crd.Spec.Versions, 1)
	schema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["capabilities"]
	var internalSchema apiextensions.JSONSchemaProps
	require.NoError(t, apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(&schema, &internalSchema, nil))
	validator, _, err := validation.NewSchemaValidator(&internalSchema)
	require.NoError(t, err)

	for _, tc := range []struct {
		name  string
		dch   string
		valid bool
	}{
		{"legacy CR", "", true},
		{"managed", `{"managementState":"Managed"}`, true},
		{"removed", `{"managementState":"Removed"}`, true},
		{"unsupported state", `{"managementState":"Unmanaged"}`, false},
		{"missing state", `{}`, false},
		{"gateway requires namespace", `{"managementState":"Managed","gateway":{"name":"gateway"}}`, false},
		{"empty audience", `{"managementState":"Managed","tokenReviewAudiences":[""]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capabilities := map[string]any{
				"featureStore": map[string]any{"managementState": string(CapabilityRemoved)},
				"dataRegistry": map[string]any{"managementState": string(CapabilityRemoved)},
			}
			if tc.dch != "" {
				var dch map[string]any
				require.NoError(t, json.Unmarshal([]byte(tc.dch), &dch))
				capabilities["dataConnectHub"] = dch
			}
			errs := validation.ValidateCustomResource(field.NewPath("spec", "capabilities"), capabilities, validator)
			if tc.valid {
				require.Empty(t, errs)
			} else {
				require.NotEmpty(t, errs)
			}
		})
	}
}
