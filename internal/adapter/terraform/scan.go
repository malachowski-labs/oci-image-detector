package terraform

import (
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/malachowski-labs/oci-image-detector/pkg/domain"
	"github.com/malachowski-labs/oci-image-detector/pkg/imageref"
)

// maxFindingsPerAttr caps the findings a single attribute may produce.
const maxFindingsPerAttr = 1000

// unresolvedMark stands in for a template interpolation that could not be
// resolved. It cannot be part of an image reference, so a reference is never
// stitched together across the gap.
const unresolvedMark = "${}"

// imageRefsIn returns every OCI image reference found in s, applying the shared
// strict precision filter (imageref.Candidates → Parse).
func imageRefsIn(s string) []domain.ImageRef {
	var refs []domain.ImageRef
	for _, raw := range imageref.Candidates(s) {
		refs = append(refs, imageref.Parse(raw))
	}
	return refs
}

// scanFile returns the image references found in one .tf file.
//
// A file with syntax errors is additionally scanned line by line for literal
// references, because the parser's partial recovery gives no guarantee about
// what it dropped.
func scanFile(f parsedFile, sc *scope) []domain.Finding {
	s := &scanner{file: f, scope: sc}
	if f.body != nil {
		s.body(f.body)
	}
	if !f.broken {
		return s.findings
	}

	type key struct {
		raw  string
		line uint
	}
	seen := make(map[key]bool, len(s.findings))
	for _, fd := range s.findings {
		seen[key{fd.Ref.Raw, fd.Line}] = true
	}
	for i, line := range f.lines {
		s.remaining = maxFindingsPerAttr
		for _, ref := range imageRefsIn(line) {
			if k := (key{ref.Raw, uint(i + 1)}); !seen[k] {
				seen[k] = true
				s.report(ref.Raw, i+1)
			}
		}
	}
	return s.findings
}

// scanner walks the expressions of one file and collects findings.
//
// Rather than evaluating an attribute as a whole, it descends to the smallest
// expressions that produce a string and evaluates those. This keeps one
// unresolvable reference from hiding the literal images around it, and lets
// every finding (and every suppress annotation) be attributed to the source
// line the image actually appears on.
type scanner struct {
	file      parsedFile
	scope     *scope
	findings  []domain.Finding
	remaining int // findings the current attribute may still produce
}

// body scans every attribute in body and its nested blocks. Attributes are
// visited in source order so that results are deterministic when a limit cuts
// the scan short.
func (s *scanner) body(body *hclsyntax.Body) {
	attrs := make([]*hclsyntax.Attribute, 0, len(body.Attributes))
	for _, attr := range body.Attributes {
		attrs = append(attrs, attr)
	}
	sort.Slice(attrs, func(i, j int) bool {
		return attrs[i].SrcRange.Start.Byte < attrs[j].SrcRange.Start.Byte
	})
	for _, attr := range attrs {
		s.remaining = maxFindingsPerAttr
		s.expr(attr.Expr)
	}
	for _, block := range body.Blocks {
		s.body(block.Body)
	}
}

// expr scans a single expression. Containers that merely hold other values are
// descended into; anything else is evaluated as a unit.
func (s *scanner) expr(expr hclsyntax.Expression) {
	switch e := expr.(type) {
	case *hclsyntax.TupleConsExpr:
		s.exprs(e.Exprs...)
	case *hclsyntax.ObjectConsExpr:
		for _, item := range e.Items {
			s.exprs(item.KeyExpr, item.ValueExpr)
		}
	case *hclsyntax.ParenthesesExpr:
		s.expr(e.Expression)
	case *hclsyntax.TemplateWrapExpr:
		s.expr(e.Wrapped)
	case *hclsyntax.ConditionalExpr:
		// Either branch may be deployed, so both are reported regardless of
		// how the condition resolves with the values at hand.
		s.exprs(e.TrueResult, e.FalseResult)
	case *hclsyntax.TemplateExpr:
		s.template(e)
	case *hclsyntax.FunctionCallExpr:
		if e.Name == "jsonencode" {
			// jsonencode preserves the strings of its argument, so scanning
			// the argument finds the same images at their own lines.
			s.exprs(e.Args...)
			return
		}
		s.evaluate(e)
	default:
		s.evaluate(e)
	}
}

func (s *scanner) exprs(exprs ...hclsyntax.Expression) {
	for _, e := range exprs {
		s.expr(e)
	}
}

// evaluate resolves expr and reports the images in its value at the line the
// expression starts on. If it cannot be resolved, its sub-expressions are
// scanned instead so that literal images inside it are still found.
func (s *scanner) evaluate(expr hclsyntax.Expression) {
	v, ok := s.scope.eval(expr)
	if !ok {
		s.exprs(subExpressions(expr)...)
		return
	}
	line := expr.Range().Start.Line
	for _, str := range stringLeaves(v.val) {
		s.report(str, line)
	}
}

// subExpressions returns the direct operands of the expression kinds that
// evaluate treats as a unit.
func subExpressions(expr hclsyntax.Expression) []hclsyntax.Expression {
	switch e := expr.(type) {
	case *hclsyntax.FunctionCallExpr:
		return e.Args
	case *hclsyntax.BinaryOpExpr:
		return []hclsyntax.Expression{e.LHS, e.RHS}
	case *hclsyntax.UnaryOpExpr:
		return []hclsyntax.Expression{e.Val}
	case *hclsyntax.IndexExpr:
		return []hclsyntax.Expression{e.Collection, e.Key}
	case *hclsyntax.RelativeTraversalExpr:
		return []hclsyntax.Expression{e.Source}
	case *hclsyntax.SplatExpr:
		return []hclsyntax.Expression{e.Source}
	case *hclsyntax.ForExpr:
		return []hclsyntax.Expression{e.CollExpr}
	case *hclsyntax.TemplateJoinExpr:
		return []hclsyntax.Expression{e.Tuple}
	}
	return nil
}

// template renders a string template part by part, tracking which source line
// each rendered line comes from, so that an image inside a multi-line string
// (typically a heredoc) is reported at its own line. An interpolation that
// cannot be resolved leaves a gap; the literal text around it is still scanned.
func (s *scanner) template(tpl *hclsyntax.TemplateExpr) {
	var (
		cur  strings.Builder
		line int
	)
	add := func(text string, at int) {
		if cur.Len() == 0 {
			line = at
		}
		cur.WriteString(text)
	}
	flush := func() {
		s.report(cur.String(), line)
		cur.Reset()
	}

	for _, part := range tpl.Parts {
		rng := part.Range()
		lit, isLit := part.(*hclsyntax.LiteralValueExpr)
		if !isLit {
			if str, ok := s.evalString(part); ok {
				add(str, rng.Start.Line)
			} else {
				add(unresolvedMark, rng.Start.Line)
				s.exprs(subExpressions(part)...)
			}
			continue
		}
		if lit.Val.IsNull() || lit.Val.Type() != cty.String {
			continue
		}
		// A newline in the literal ends the rendered line. An escaped "\n"
		// does so without advancing the source line, hence the clamp.
		for i, chunk := range strings.Split(lit.Val.AsString(), "\n") {
			if i > 0 {
				flush()
			}
			add(chunk, min(rng.Start.Line+i, rng.End.Line))
		}
	}
	flush()
}

// evalString resolves a template interpolation to the string it renders as.
func (s *scanner) evalString(expr hclsyntax.Expression) (string, bool) {
	v, ok := s.scope.eval(expr)
	if !ok || v.val.IsNull() {
		return "", false
	}
	str, err := convert.Convert(v.val, cty.String)
	if err != nil {
		return "", false
	}
	return str.AsString(), true
}

// report emits a finding for each image reference in str, attributed to the
// given source line, unless that line carries the inline suppress annotation
// (suppress.IsIgnoredLine).
func (s *scanner) report(str string, line int) {
	if isIgnoredLine(s.file.lines, line) {
		return
	}
	for _, ref := range imageRefsIn(str) {
		if s.remaining == 0 {
			s.scope.limited = true
			return
		}
		s.remaining--
		s.findings = append(s.findings, domain.Finding{
			Ref:      ref,
			FilePath: s.file.name,
			Line:     uint(line),
			Strategy: Strategy,
		})
	}
}

// stringLeaves collects every string value reachable within v, descending into
// lists, sets, tuples, maps and objects.
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
