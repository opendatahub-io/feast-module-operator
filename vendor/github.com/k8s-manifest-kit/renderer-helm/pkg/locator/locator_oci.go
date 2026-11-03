package locator

import (
	"context"
	"errors"
	"fmt"

	"github.com/k8s-manifest-kit/renderer-helm/pkg/container"
)

// ErrEmptyChartData is returned when an OCI pull returns no chart content.
var ErrEmptyChartData = errors.New("OCI pull returned empty chart data")

// Re-export container sentinel errors so existing locator consumers don't break.
var (
	ErrNoTags            = container.ErrNoTags
	ErrRefContainsTag    = container.ErrRefContainsTag
	ErrRefContainsDigest = container.ErrRefContainsDigest
	ErrNoValidSemverTag  = container.ErrNoValidSemverTag
)

// OCI resolves charts stored in an OCI-compatible registry.
type OCI struct {
	Ref         string
	Version     string
	Credentials *Credentials
	CacheDir    string
	PlainHTTP   bool
}

// Locate pulls the chart from an OCI registry and returns the local cache path.
func (o *OCI) Locate(ctx context.Context) (Result, error) {
	if o.CacheDir == "" {
		return Result{}, ErrEmptyCacheDir
	}

	data, err := o.pull(ctx)
	if err != nil {
		return Result{}, err
	}

	path, err := cacheChart(o.CacheDir, data)
	if err != nil {
		return Result{}, err
	}

	return Result{Path: path, SourceType: SourceOCI}, nil
}

func (o *OCI) pull(ctx context.Context) ([]byte, error) {
	var opts []container.ClientOption
	if o.Credentials.hasAuth() {
		opts = append(opts, container.WithCredential(o.Credentials.Username, o.Credentials.Password))
	}

	if o.PlainHTTP {
		opts = append(opts, container.WithPlainHTTP(true))
	}

	client, err := container.NewClient(o.Ref, opts...)
	if err != nil {
		return nil, fmt.Errorf("unable to create OCI client: %w", err)
	}

	if dgst := container.EmbeddedDigest(o.Ref); dgst != "" {
		if o.Version != "" {
			return nil, fmt.Errorf("%w: ref %q has digest %q, version %q", ErrRefContainsDigest, o.Ref, dgst, o.Version)
		}

		data, err := client.PullDigest(ctx, dgst)
		if err != nil {
			return nil, fmt.Errorf("unable to pull chart by digest: %w", err)
		}

		if len(data) == 0 {
			return nil, fmt.Errorf("%w: %q", ErrEmptyChartData, o.Ref)
		}

		return data, nil
	}

	tag, err := client.ResolveTag(ctx, o.Version)
	if err != nil {
		return nil, fmt.Errorf("unable to resolve tag: %w", err)
	}

	data, err := client.Pull(ctx, tag)
	if err != nil {
		return nil, fmt.Errorf("unable to pull chart by tag: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrEmptyChartData, o.Ref)
	}

	return data, nil
}
