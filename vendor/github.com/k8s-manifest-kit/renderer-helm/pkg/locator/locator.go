package locator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrPathNotFound is returned when a chart path does not exist on the local filesystem.
	ErrPathNotFound = errors.New("path not found")

	// ErrNilRequest is returned when Locate receives a nil request.
	ErrNilRequest = errors.New("request must not be nil")

	// ErrEmptyCacheDir is returned when a locator requires a cache directory but none was provided.
	ErrEmptyCacheDir = errors.New("cache directory must not be empty")
)

const (
	dirPermissions  = 0750
	filePermissions = 0600
)

// Locator resolves a chart reference to a local filesystem path.
type Locator interface {
	Locate(ctx context.Context) (Result, error)
}

// Credentials holds authentication credentials for accessing Helm repositories and registries.
type Credentials struct {
	Username string
	Password string //nolint:gosec // not a hardcoded credential, just a field name
}

func (c *Credentials) hasAuth() bool {
	return c != nil && (c.Username != "" || c.Password != "")
}

// Request describes a chart to locate along with the infrastructure paths
// needed for downloading. Credentials and the OCI registry client are resolved
// lazily inside Locate -- only when the chart actually requires downloading.
type Request struct {
	Name    string
	RepoURL string
	Version string

	// Credentials is called lazily during Locate to obtain authentication.
	// Nil means no authentication.
	Credentials func(context.Context) (*Credentials, error)

	RepositoryCache string
}

func (r *Request) resolveCredentials(ctx context.Context) (*Credentials, error) {
	if r.Credentials == nil {
		return nil, nil //nolint:nilnil // nil credentials means no authentication
	}

	creds, err := r.Credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials: %w", err)
	}

	return creds, nil
}

// Locate resolves a chart reference to a local filesystem path.
//
// Resolution order:
//  1. When no RepoURL is set, check if Name is a local path.
//  2. If the path is absolute or starts with '.', error when it does not exist.
//  3. Otherwise download via the appropriate locator (Repo or OCI).
func Locate(ctx context.Context, req *Request) (Result, error) {
	if req == nil {
		return Result{}, ErrNilRequest
	}

	l, err := newLocator(ctx, req)
	if err != nil {
		return Result{}, err
	}

	result, err := l.Locate(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("failed to locate chart %q: %w", req.Name, err)
	}

	return result, nil
}

func newLocator(ctx context.Context, req *Request) (Locator, error) {
	name := strings.TrimSpace(req.Name)
	version := strings.TrimSpace(req.Version)

	if req.RepoURL == "" {
		if _, err := os.Stat(name); err == nil {
			return &Local{Name: name}, nil
		}

		if filepath.IsAbs(name) || strings.HasPrefix(name, ".") {
			return &Local{Name: name, MustExist: true}, nil
		}
	}

	creds, err := req.resolveCredentials(ctx)
	if err != nil {
		return nil, err
	}

	var locator Locator
	if strings.HasPrefix(name, "oci://") {
		locator = &OCI{
			Ref:         name,
			Version:     version,
			Credentials: creds,
			CacheDir:    req.RepositoryCache,
		}
	} else {
		locator = &Repo{
			Name:        name,
			RepoURL:     req.RepoURL,
			Version:     version,
			Credentials: creds,
			CacheDir:    req.RepositoryCache,
		}
	}

	return locator, nil
}

// Local resolves chart references that point to the local filesystem.
type Local struct {
	Name      string
	MustExist bool
}

// Locate returns the absolute path to a local chart, or an error if MustExist
// is set and the path was not found.
func (l *Local) Locate(_ context.Context) (Result, error) {
	if l.MustExist {
		return Result{}, fmt.Errorf("%w: %s", ErrPathNotFound, l.Name)
	}

	abs, err := filepath.Abs(l.Name)
	if err != nil {
		return Result{}, fmt.Errorf("unable to resolve absolute path for %q: %w", l.Name, err)
	}

	return Result{Path: abs, SourceType: SourceLocal}, nil
}
