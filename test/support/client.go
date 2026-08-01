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

package support

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	componentsv1alpha1 "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
)

// ClusterClient wraps a kubernetes.Clientset with additional helper methods
// for feast-module-operator testing.
type ClusterClient struct {
	*kubernetes.Clientset
	config *rest.Config
}

// NewClusterClient creates a new ClusterClient from the current kubeconfig.
func NewClusterClient() (ClusterClient, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return ClusterClient{}, err
	}

	// Set a reasonable timeout if not already configured to prevent unbounded waits
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return ClusterClient{}, err
	}

	return ClusterClient{
		Clientset: clientset,
		config:    cfg,
	}, nil
}

// CreateFeastOperator creates a FeastOperator CR in the cluster using dynamic client.
func (c ClusterClient) CreateFeastOperator(ctx context.Context, name string) error {
	dynamicClient, err := dynamic.NewForConfig(c.config)
	if err != nil {
		return err
	}

	feastGVR := schema.GroupVersionResource{
		Group:    "components.platform.opendatahub.io",
		Version:  "v1alpha1",
		Resource: "feastoperators",
	}

	feastCR := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "components.platform.opendatahub.io/v1alpha1",
			"kind":       "FeastOperator",
			"metadata": map[string]interface{}{
				"name": name,
			},
			"spec": map[string]interface{}{},
		},
	}

	_, err = dynamicClient.Resource(feastGVR).Create(ctx, feastCR, metav1.CreateOptions{})
	return err
}

// GetFeastOperator retrieves a FeastOperator CR from the cluster using dynamic client.
func (c ClusterClient) GetFeastOperator(ctx context.Context, name string) (*componentsv1alpha1.FeastOperator, error) {
	dynamicClient, err := dynamic.NewForConfig(c.config)
	if err != nil {
		return nil, err
	}

	feastGVR := schema.GroupVersionResource{
		Group:    "components.platform.opendatahub.io",
		Version:  "v1alpha1",
		Resource: "feastoperators",
	}

	unstructuredObj, err := dynamicClient.Resource(feastGVR).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	feastCR := &componentsv1alpha1.FeastOperator{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, feastCR)
	return feastCR, err
}
