package types

import (
	"github.com/k8s-manifest-kit/pkg/util/k8s"
)

const (
	// AnnotationSourceType is the annotation key for the renderer type.
	AnnotationSourceType = "manifests.k8s-manifests-kit/source.type"

	// AnnotationSourcePath is the annotation key for the source path/chart identifier.
	AnnotationSourcePath = "manifests.k8s-manifests-kit/source.path"

	// AnnotationSourceFile is the annotation key for the specific template file.
	AnnotationSourceFile = "manifests.k8s-manifests-kit/source.file"

	// AnnotationContentHash is the annotation key for the SHA-256 content hash of the rendered resource.
	AnnotationContentHash = "manifests.k8s-manifests-kit/content.hash"
)

// SetContentHash computes a deterministic content hash for the given object
// and stores it as the AnnotationContentHash annotation.
func SetContentHash(obj k8s.Object) {
	k8s.SetAnnotation(obj, AnnotationContentHash, k8s.ContentHash(obj))
}
