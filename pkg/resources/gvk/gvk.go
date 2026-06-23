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

package gvk

import (
	clustergvk "github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
)

// Chart generation GVKs re-exported from the upstream operator cluster package.
var (
	Namespace                      = clustergvk.Namespace
	Deployment                     = clustergvk.Deployment
	ServiceAccount                 = clustergvk.ServiceAccount
	ConfigMap                      = clustergvk.ConfigMap
	ClusterRoleBinding             = clustergvk.ClusterRoleBinding
	RoleBinding                    = clustergvk.RoleBinding
	MutatingWebhookConfiguration   = clustergvk.MutatingWebhookConfiguration
	ValidatingWebhookConfiguration = clustergvk.ValidatingWebhookConfiguration
	CertManagerCertificate         = clustergvk.CertManagerCertificate
)
