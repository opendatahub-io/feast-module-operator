package container

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/distribution/reference"
	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

const (
	chartLayerMediaType         = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
	legacyChartLayerMediaType   = "application/tar+gzip"
	dockerManifestListMediaType = "application/vnd.docker.distribution.manifest.list.v2+json"

	maxBlobSize = 256 << 20 // 256 MiB hard upper bound for blob downloads
)

// computeCredentials returns a credential function for OCI registry auth.
// When no explicit credential is provided, it attempts to load the Docker
// credential store. Initialization failures are intentionally swallowed
// (falling back to empty credentials) so that pulls from public registries
// work even when no Docker config exists. AllowPlaintextPut is required by
// the oras credentials API for read-only store access; it does not affect
// security since this client never writes credentials.
func computeCredentials(named reference.Named, opts *ClientOptions) auth.CredentialFunc {
	if opts.credential != nil {
		host := reference.Domain(named)

		return auth.StaticCredential(host, *opts.credential)
	}

	store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{
		AllowPlaintextPut:        true,
		DetectDefaultNativeStore: true,
	})
	if err != nil {
		return func(_ context.Context, _ string) (auth.Credential, error) {
			return auth.EmptyCredential, nil
		}
	}

	return credentials.Credential(store)
}

func listTags(ctx context.Context, repo *remote.Repository) ([]string, error) {
	var tags []string

	err := repo.Tags(ctx, "", func(page []string) error {
		for _, tag := range page {
			tags = append(tags, strings.ReplaceAll(tag, "_", "+"))
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}

	return tags, nil
}

func fetchManifest(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor) (*resolvedArtifact, error) {
	manifest, err := fetch[ocispec.Manifest](ctx, repo, desc)
	if err != nil {
		return nil, fmt.Errorf("unable to fetch manifest: %w", err)
	}

	return &resolvedArtifact{desc: desc, manifest: manifest}, nil
}

func fetchBlob(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor) ([]byte, error) {
	if desc.Size <= 0 {
		return nil, fmt.Errorf("%w: %s reports size %d", ErrInvalidDescriptorSize, desc.Digest, desc.Size)
	}

	if desc.Size > maxBlobSize {
		return nil, fmt.Errorf("%w: %s is %d bytes (max %d)", ErrBlobTooLarge, desc.Digest, desc.Size, maxBlobSize)
	}

	rc, err := repo.Fetch(ctx, desc)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", desc.Digest, err)
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(io.LimitReader(rc, desc.Size))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", desc.Digest, err)
	}

	return data, nil
}

func fetch[T any](ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor) (T, error) {
	var zero T

	rc, err := repo.Fetch(ctx, desc)
	if err != nil {
		return zero, fmt.Errorf("fetch %s: %w", desc.Digest, err)
	}
	defer func() { _ = rc.Close() }()

	var target T
	if err := json.NewDecoder(rc).Decode(&target); err != nil {
		return zero, fmt.Errorf("decode %s: %w", desc.Digest, err)
	}

	return target, nil
}

func isIndexType(mediaType string) bool {
	return mediaType == ocispec.MediaTypeImageIndex || mediaType == dockerManifestListMediaType
}

func isChartType(mediaType string) bool {
	return mediaType == chartLayerMediaType || mediaType == legacyChartLayerMediaType
}

// findChartLayer returns the last layer with a recognised Helm chart media
// type, or nil if the manifest contains no chart layer.  The last match is
// chosen because Helm's own push places the chart content as the final layer,
// and some tooling prepends non-chart layers.
func findChartLayer(manifest ocispec.Manifest) *ocispec.Descriptor {
	var layer *ocispec.Descriptor
	for i := range manifest.Layers {
		if isChartType(manifest.Layers[i].MediaType) {
			layer = &manifest.Layers[i]
		}
	}

	return layer
}

// resolveTag determines the tag to pull. Digest refs are never passed here --
// the caller (locator_oci.pull) checks for an embedded digest first and routes
// to PullDigest, bypassing tag resolution entirely.
func resolveTag(
	ctx context.Context,
	ref string,
	version string,
	listTagsFn func(context.Context) ([]string, error),
) (string, error) {
	tag := EmbeddedTag(ref)

	switch {
	case tag != "" && version != "":
		return "", fmt.Errorf("%w: ref %q has tag %q, version %q", ErrRefContainsTag, ref, tag, version)
	case version != "":
		return version, nil
	case tag != "":
		return tag, nil
	}

	tags, err := listTagsFn(ctx)

	switch {
	case err != nil:
		return "", fmt.Errorf("unable to list tags for %q: %w", ref, err)
	case len(tags) == 0:
		return "", fmt.Errorf("%w: %q", ErrNoTags, ref)
	}

	latest, err := latestSemver(tags)
	if err != nil {
		return "", fmt.Errorf("unable to resolve latest version for %q: %w", ref, err)
	}

	return latest.Original(), nil
}

func latestSemver(tags []string) (*semver.Version, error) {
	versions := make([]*semver.Version, 0, len(tags))

	for _, t := range tags {
		v, err := semver.NewVersion(t)
		if err != nil {
			continue
		}

		versions = append(versions, v)
	}

	if len(versions) == 0 {
		return nil, ErrNoValidSemverTag
	}

	sort.Sort(sort.Reverse(semver.Collection(versions)))

	return versions[0], nil
}

// EmbeddedTag extracts a tag from an OCI ref if present.
// e.g. "oci://registry/chart:1.0" returns "1.0", "oci://registry/chart" returns "".
// Port numbers (e.g. "localhost:5000/chart") are not mistaken for tags.
func EmbeddedTag(ref string) string {
	named, err := reference.ParseNormalizedNamed(strings.TrimPrefix(ref, "oci://"))
	if err != nil {
		return ""
	}

	tagged, ok := named.(reference.Tagged)
	if !ok {
		return ""
	}

	return tagged.Tag()
}

// EmbeddedDigest extracts a digest from an OCI ref if present.
// e.g. "oci://registry/chart@sha256:abc..." returns the digest,
// "oci://registry/chart:1.0" returns "".
func EmbeddedDigest(ref string) godigest.Digest {
	named, err := reference.ParseNormalizedNamed(strings.TrimPrefix(ref, "oci://"))
	if err != nil {
		return ""
	}

	digested, ok := named.(reference.Digested)
	if !ok {
		return ""
	}

	return digested.Digest()
}
