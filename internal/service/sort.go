package service

import (
	"cmp"
	"slices"

	"github.com/malachowski-labs/oci-image-detector/pkg/domain"
)

// sortFindings sorts findings in-place by FilePath → Line → Raw.
// This is called by ScanService before handing results to a Reporter,
// guaranteeing deterministic output regardless of traversal order.
func sortFindings(findings []domain.Finding) {
	slices.SortFunc(findings, func(a, b domain.Finding) int {
		return cmp.Or(
			cmp.Compare(a.FilePath, b.FilePath),
			cmp.Compare(a.Line, b.Line),
			cmp.Compare(a.Ref.Raw, b.Ref.Raw),
		)
	})
}
