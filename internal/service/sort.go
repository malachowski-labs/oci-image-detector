package service

import (
	"sort"

	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// sortFindings sorts findings in-place by FilePath → Line → Raw.
// This is called by ScanService before handing results to a Reporter,
// guaranteeing deterministic output regardless of traversal order.
func sortFindings(findings []domain.Finding) {
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.FilePath != b.FilePath {
			return a.FilePath < b.FilePath
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Ref.Raw < b.Ref.Raw
	})
}
