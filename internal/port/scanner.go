package port

import (
	"context"

	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// ScanOptions controls the behaviour of a Scanner.Scan call.
// Using a struct rather than positional parameters keeps the Scanner interface
// stable as new options are added.
type ScanOptions struct {
	// Exclude is the list of glob patterns for paths to skip.
	// Patterns are matched against slash-separated paths relative to the scan
	// root using doublestar syntax (e.g. "**/*.tf", "vendor/**").
	// An empty slice means nothing is excluded.
	Exclude []string
}

// Scanner is the driving port called by the CLI adapter to trigger a scan.
// The concrete implementation lives in internal/service.
type Scanner interface {
	// Scan walks dir recursively, applying opts, and returns every image
	// reference found across all files.
	//
	// The returned slice is sorted by FilePath → Line → Raw via
	// domain.SortFindings for deterministic output regardless of traversal order.
	//
	// An empty result is not an error; callers decide whether that is
	// acceptable (see --allow-empty).
	Scan(ctx context.Context, dir string, opts ScanOptions) ([]domain.Finding, error)
}
