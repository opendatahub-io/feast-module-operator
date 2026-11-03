package locator

// SourceType indicates how a chart was resolved.
type SourceType string

const (
	// SourceLocal indicates the chart was resolved from the local filesystem.
	SourceLocal SourceType = "local"
	// SourceOCI indicates the chart was pulled from an OCI registry.
	SourceOCI SourceType = "oci"
	// SourceRepo indicates the chart was downloaded from a Helm HTTP repository.
	SourceRepo SourceType = "repo"
)

// Result is the outcome of a Locate call.
type Result struct {
	Path       string
	SourceType SourceType
}
