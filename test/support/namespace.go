package support

import "os"

const (
	// DefaultOperatorNamespace is where the module operator and feast-operator
	// operands run on RHOAI. Override with OPERATOR_NAMESPACE for Helm-local
	// e2e (opendatahub-feast-system).
	DefaultOperatorNamespace        = "redhat-ods-applications"
	DefaultIntegrationTestNamespace = "integration-test"
	DefaultApplicationsNamespace    = "redhat-ods-applications"
)

func OperatorNamespace() string {
	if namespace := os.Getenv("OPERATOR_NAMESPACE"); namespace != "" {
		return namespace
	}

	return DefaultOperatorNamespace
}

func IntegrationTestNamespace() string {
	if namespace := os.Getenv("INTEGRATION_TEST_NAMESPACE"); namespace != "" {
		return namespace
	}

	return DefaultIntegrationTestNamespace
}

// HelmNamespace returns the operator namespace used by e2e and Helm deploy targets.
func HelmNamespace() string {
	return OperatorNamespace()
}
