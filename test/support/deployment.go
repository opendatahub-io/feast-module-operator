package support

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
)

// ContainerImage returns the image configured for a named container in a
// Deployment's pod template.
func ContainerImage(deployment *appsv1.Deployment, containerName string) (string, error) {
	if deployment == nil {
		return "", fmt.Errorf("deployment is nil")
	}

	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == containerName {
			if container.Image == "" {
				return "", fmt.Errorf("container %q has no image", containerName)
			}
			return container.Image, nil
		}
	}

	return "", fmt.Errorf("container %q not found in deployment %q", containerName, deployment.Name)
}

// VerifyContainerImage checks that a Deployment's named container runs exactly
// the expected image reference.
func VerifyContainerImage(deployment *appsv1.Deployment, containerName, expectedImage string) error {
	actualImage, err := ContainerImage(deployment, containerName)
	if err != nil {
		return err
	}

	if actualImage != expectedImage {
		return fmt.Errorf("deployment %q container %q runs %q, expected %q", deployment.Name, containerName, actualImage, expectedImage)
	}

	return nil
}
