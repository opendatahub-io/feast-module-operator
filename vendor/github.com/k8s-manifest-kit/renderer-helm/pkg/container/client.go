package container

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/distribution/reference"
	godigest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
)

// Client wraps an ORAS remote.Repository for Helm chart OCI operations.
type Client struct {
	ref         string
	originalRef string
	repo        *remote.Repository
}

// NewClient creates a new OCI registry client for the given reference.
// The ref may include an oci:// prefix and/or an embedded tag; both are
// stripped. Tags are passed separately to Pull.
func NewClient(ref string, opts ...ClientOption) (*Client, error) {
	var options ClientOptions
	for _, opt := range opts {
		opt.ApplyTo(&options)
	}

	bareRef := strings.TrimPrefix(ref, "oci://")

	named, err := reference.ParseNormalizedNamed(bareRef)
	if err != nil {
		return nil, fmt.Errorf("invalid OCI reference %q: %w", ref, err)
	}

	repoRef := reference.Domain(named) + "/" + reference.Path(named)

	repo, err := remote.NewRepository(repoRef)
	if err != nil {
		return nil, fmt.Errorf("unable to create repository for %q: %w", ref, err)
	}

	repo.PlainHTTP = options.PlainHTTP
	repo.Client = &auth.Client{Credential: computeCredentials(named, &options)}

	return &Client{ref: repoRef, originalRef: ref, repo: repo}, nil
}

// Pull fetches the Helm chart .tgz bytes for the given tag.
// Only the chart layer is downloaded; config and provenance layers are skipped.
func (c *Client) Pull(ctx context.Context, tag string) ([]byte, error) {
	if tag == "" {
		return nil, fmt.Errorf("%w: %q", ErrEmptyTag, c.ref)
	}

	fullRef := c.ref + ":" + tag

	return c.fetchChart(ctx, fullRef)
}

// PullDigest fetches the Helm chart .tgz bytes for the given manifest digest,
// ensuring reproducible pulls that are immune to tag mutation.
func (c *Client) PullDigest(ctx context.Context, dgst godigest.Digest) ([]byte, error) {
	if err := dgst.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidDigest, err)
	}

	fullRef := c.ref + "@" + dgst.String()

	return c.fetchChart(ctx, fullRef)
}

// Tags returns all tags from the repository, converting OCI tag underscores
// back to plus signs for semver compatibility.
// See https://github.com/helm/helm/issues/10166
func (c *Client) Tags(ctx context.Context) ([]string, error) {
	tags, err := listTags(ctx, c.repo)
	if err != nil {
		return nil, fmt.Errorf("unable to list tags for %q: %w", c.ref, err)
	}

	return tags, nil
}

// ResolveTag determines the tag to pull for this client's reference.
// It checks for embedded tags, explicit version overrides, and falls back
// to listing tags from the registry to pick the latest semver.
func (c *Client) ResolveTag(ctx context.Context, version string) (string, error) {
	return resolveTag(ctx, c.originalRef, version, c.Tags)
}

func (c *Client) fetchChart(ctx context.Context, fullRef string) ([]byte, error) {
	resolved, err := c.resolveManifest(ctx, fullRef)
	if err != nil {
		return nil, err
	}

	chartLayer := findChartLayer(resolved.manifest)
	if chartLayer == nil {
		return nil, fmt.Errorf("%w: %q", ErrNoChartLayer, fullRef)
	}

	data, err := fetchBlob(ctx, c.repo, *chartLayer)
	if err != nil {
		return nil, fmt.Errorf("unable to fetch chart layer for %q: %w", fullRef, err)
	}

	return data, nil
}

// resolveManifest resolves the reference and fetches its manifest.
// If the descriptor points to an OCI image index (manifest list), it
// dereferences the index to the first entry that contains a Helm chart layer.
func (c *Client) resolveManifest(ctx context.Context, fullRef string) (*resolvedArtifact, error) {
	desc, err := c.repo.Resolve(ctx, fullRef)
	if err != nil {
		return nil, fmt.Errorf("unable to resolve %q: %w", fullRef, err)
	}

	if !isIndexType(desc.MediaType) {
		resolved, err := fetchManifest(ctx, c.repo, desc)
		if err != nil {
			return nil, fmt.Errorf("unable to fetch manifest for %q: %w", fullRef, err)
		}

		return resolved, nil
	}

	idx, err := fetch[ocispec.Index](ctx, c.repo, desc)
	if err != nil {
		return nil, fmt.Errorf("unable to fetch index for %q: %w", fullRef, err)
	}

	if len(idx.Manifests) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrEmptyIndex, fullRef)
	}

	var probeErrs []error
	var hadSuccessfulProbe bool

	for _, candidate := range idx.Manifests {
		resolved, probeErr := fetchManifest(ctx, c.repo, candidate)
		if probeErr != nil {
			probeErrs = append(probeErrs, probeErr)

			continue
		}

		hadSuccessfulProbe = true

		if findChartLayer(resolved.manifest) != nil {
			return resolved, nil
		}
	}

	if !hadSuccessfulProbe && len(probeErrs) > 0 {
		return nil, fmt.Errorf("all index candidates failed for %q: %w", fullRef, errors.Join(probeErrs...))
	}

	return nil, fmt.Errorf("%w: %q", ErrNoChartLayer, fullRef)
}
