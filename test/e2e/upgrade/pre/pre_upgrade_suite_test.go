//go:build e2e

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

package pre

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/opendatahub-io/feast-module-operator/test/support"
)

var (
	k8sClient support.ClusterClient
	ctx       context.Context
	namespace string
)

func TestMain(m *testing.M) {
	fmt.Fprintf(os.Stderr, "Starting pre-upgrade smoke test suite\n")

	SetDefaultEventuallyTimeout(5 * time.Minute)
	SetDefaultEventuallyPollingInterval(1 * time.Second)

	ctx = context.Background()
	namespace = support.DefaultApplicationsNamespace

	var err error
	k8sClient, err = support.NewClusterClient()
	if err != nil {
		log.Fatalf("Failed to create cluster client: %v", err)
	}

	// Pre-flight: verify operator is deployed before running pre-upgrade tests
	pods, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "control-plane=controller-manager,app.kubernetes.io/name=feast-operator",
	})
	if err != nil || len(pods.Items) == 0 {
		log.Fatalf("No feast-operator controller-manager pod found in %s — deploy the operator before running pre-upgrade tests", namespace)
	}

	os.Exit(m.Run())
}
