package terraform

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/imageref"
	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// imageRefsIn returns every OCI image reference found in s, applying the shared
// strict precision filter (imageref.Candidates → Parse → LooksLikeImage). It is
// the single source of truth for turning a concrete, fully-resolved string into
// image findings, reused by both the HCL detector and the plan command.
func imageRefsIn(s string) []domain.ImageRef {
	var refs []domain.ImageRef
	for _, raw := range imageref.Candidates(s) {
		ref := imageref.Parse(raw)
		if imageref.LooksLikeImage(ref) {
			refs = append(refs, ref)
		}
	}
	return refs
}

// scanBody recursively evaluates every attribute in body (and its nested blocks)
// against ctx and emits a finding for each image reference found in any resolved
// string value. Attributes whose expressions cannot be fully resolved (they
// reference resources, data sources, unknown functions or absent variables) are
// silently skipped — this preserves the detector's precision contract: only
// concrete, resolved image strings are reported.
//
// An attribute whose source line carries the inline suppress annotation
// (imageref.IsIgnoredLine, e.g. `image = "…"  # oci-image-detector:ignore`) is
// skipped, matching the per-line suppression the other detectors honor.
func scanBody(filename string, body *hclsyntax.Body, ctx *hcl.EvalContext, lines []string) []domain.Finding {
	var findings []domain.Finding

	for _, attr := range body.Attributes {
		if isSuppressed(attr, lines) {
			continue
		}
		v, diags := attr.Expr.Value(ctx)
		if diags.HasErrors() || v.IsNull() || !v.IsWhollyKnown() {
			continue
		}
		line := uint(attr.SrcRange.Start.Line)
		for _, s := range stringLeaves(v) {
			for _, ref := range imageRefsIn(s) {
				findings = append(findings, domain.Finding{
					Ref:      ref,
					FilePath: filename,
					Line:     line,
					Strategy: Strategy,
				})
			}
		}
	}

	for _, block := range body.Blocks {
		findings = append(findings, scanBody(filename, block.Body, ctx, lines)...)
	}

	return findings
}

// isSuppressed reports whether the attribute's source line carries the inline
// suppress annotation. It uses the attribute's start line (1-based) as the
// suppression anchor — the same physical line an author annotates.
func isSuppressed(attr *hclsyntax.Attribute, lines []string) bool {
	i := attr.SrcRange.Start.Line - 1
	if i < 0 || i >= len(lines) {
		return false
	}
	return imageref.IsIgnoredLine(lines[i])
}

// stringLeaves collects every string value reachable within v, descending into
// lists, sets, tuples, maps and objects. This lets the scanner pick image
// references out of structural values such as an ECS container_definitions
// jsonencode([...]) blob or a list/map attribute, not just top-level strings.
func stringLeaves(v cty.Value) []string {
	var out []string
	collectStringLeaves(v, &out)
	return out
}

func collectStringLeaves(v cty.Value, out *[]string) {
	if v.IsNull() || !v.IsKnown() {
		return
	}
	t := v.Type()
	switch {
	case t == cty.String:
		*out = append(*out, v.AsString())
	case t.IsListType() || t.IsSetType() || t.IsTupleType(),
		t.IsMapType() || t.IsObjectType():
		for it := v.ElementIterator(); it.Next(); {
			_, ev := it.Element()
			collectStringLeaves(ev, out)
		}
	}
}
