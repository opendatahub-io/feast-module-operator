package locator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"sigs.k8s.io/yaml"
)

var (
	// ErrNoDownloadURLs is returned when a resolved chart version has no download URLs.
	ErrNoDownloadURLs = errors.New("chart version has no download URLs")

	// ErrEmptyRepoURL is returned when a Repo locator is created without a repository URL.
	ErrEmptyRepoURL = errors.New("repository URL must not be empty")
)

// Repo resolves charts hosted in a classic Helm HTTP/HTTPS repository.
type Repo struct {
	Name        string
	RepoURL     string
	Version     string
	Credentials *Credentials
	CacheDir    string
	HTTPClient  *http.Client
}

// Locate downloads the chart from a Helm repository and returns the local cache path.
func (r *Repo) Locate(ctx context.Context) (Result, error) {
	if r.RepoURL == "" {
		return Result{}, ErrEmptyRepoURL
	}

	if r.CacheDir == "" {
		return Result{}, ErrEmptyCacheDir
	}

	chartURL, err := r.resolveChartURL(ctx)
	if err != nil {
		return Result{}, err
	}

	creds, err := r.downloadCredentials(chartURL)
	if err != nil {
		return Result{}, err
	}

	data, err := httpGet(ctx, r.HTTPClient, chartURL, creds)
	if err != nil {
		return Result{}, fmt.Errorf("unable to download chart: %w", err)
	}

	path, err := cacheChart(r.CacheDir, data)
	if err != nil {
		return Result{}, err
	}

	return Result{Path: path, SourceType: SourceRepo}, nil
}

func (r *Repo) resolveChartURL(ctx context.Context) (string, error) {
	indexURL := strings.TrimSuffix(r.RepoURL, "/") + "/index.yaml"

	data, err := httpGet(ctx, r.HTTPClient, indexURL, r.Credentials)
	if err != nil {
		return "", fmt.Errorf("unable to fetch repository index from %q: %w", indexURL, err)
	}

	var idx repoIndex
	if err := yaml.Unmarshal(data, &idx); err != nil {
		return "", fmt.Errorf("unable to parse repository index: %w", err)
	}

	cv, err := idx.resolve(r.Name, r.Version)
	if err != nil {
		return "", fmt.Errorf("unable to find chart %q in repo %q: %w", r.Name, r.RepoURL, err)
	}

	if len(cv.URLs) == 0 {
		return "", fmt.Errorf("%w: chart %q version %q", ErrNoDownloadURLs, r.Name, cv.Version)
	}

	chartURL := cv.URLs[0]

	if u, err := url.Parse(chartURL); err == nil && !u.IsAbs() {
		base, parseErr := url.Parse(strings.TrimSuffix(r.RepoURL, "/") + "/")
		if parseErr != nil {
			return "", fmt.Errorf("unable to parse repo URL %q: %w", r.RepoURL, parseErr)
		}

		chartURL = base.ResolveReference(u).String()
	}

	return chartURL, nil
}

// downloadCredentials returns credentials for the chart download, applying
// same-origin protection to prevent credential leakage across hosts.
func (r *Repo) downloadCredentials(chartURL string) (*Credentials, error) {
	if !r.Credentials.hasAuth() {
		return nil, nil //nolint:nilnil // nil credentials means no authentication
	}

	u1, err := url.Parse(r.RepoURL)
	if err != nil {
		return nil, fmt.Errorf("unable to parse repo URL %q: %w", r.RepoURL, err)
	}

	u2, err := url.Parse(chartURL)
	if err != nil {
		return nil, fmt.Errorf("unable to parse chart URL %q: %w", chartURL, err)
	}

	if !urlsShareOrigin(u1, u2) {
		return nil, nil //nolint:nilnil // different origin means no credentials
	}

	return r.Credentials, nil
}
