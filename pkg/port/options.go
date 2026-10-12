package port

// ScanOptions controls the behaviour of a scan.
// Using a struct rather than positional parameters keeps the scan signature
// stable as new options are added.
type ScanOptions struct {
	// Exclude is the list of glob patterns for paths to skip.
	// Patterns are matched against slash-separated paths relative to the scan
	// root using doublestar syntax (e.g. "**/*.tf", "vendor/**").
	// An empty slice means nothing is excluded.
	Exclude []string

	// ExcludeImages is the list of doublestar glob patterns matched against
	// the raw image reference string (Finding.Ref.Raw). Findings whose raw
	// reference matches any pattern are dropped from the output.
	// Useful for suppressing placeholder or well-known-noise images that
	// appear across many files or in generated/vendored sources.
	// Examples: "localhost:5000/**", "*.example.com/**"
	ExcludeImages []string
}
