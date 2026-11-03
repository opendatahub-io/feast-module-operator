package container

import (
	"errors"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ErrNoChartLayer is returned when the OCI manifest contains no layer with
// a recognised Helm chart media type.
var ErrNoChartLayer = errors.New("no chart layer found in OCI manifest")

// ErrEmptyIndex is returned when an OCI image index contains no manifest entries.
var ErrEmptyIndex = errors.New("OCI index contains no manifests")

// ErrEmptyTag is returned when Pull is called with an empty tag.
var ErrEmptyTag = errors.New("tag must not be empty")

// ErrInvalidDigest is returned when PullDigest is called with a malformed digest.
var ErrInvalidDigest = errors.New("invalid digest")

// ErrRefContainsTag is returned when an OCI ref already contains an embedded
// tag and a separate version is also specified.
var ErrRefContainsTag = errors.New("OCI ref already contains a tag; cannot also specify Version")

// ErrRefContainsDigest is returned when an OCI ref contains an embedded digest
// and a separate version is also specified.
var ErrRefContainsDigest = errors.New("OCI ref already contains a digest; cannot also specify Version")

// ErrNoTags is returned when a registry has no tags for the given reference.
var ErrNoTags = errors.New("no tags found")

// ErrNoValidSemverTag is returned when none of the registry tags are valid semver.
var ErrNoValidSemverTag = errors.New("no valid semver tag found")

// ErrInvalidDescriptorSize is returned when a descriptor reports a non-positive size.
var ErrInvalidDescriptorSize = errors.New("descriptor has invalid size")

// ErrBlobTooLarge is returned when a blob exceeds the maximum allowed size.
var ErrBlobTooLarge = errors.New("blob exceeds maximum allowed size")

type resolvedArtifact struct {
	desc     ocispec.Descriptor
	manifest ocispec.Manifest
}
