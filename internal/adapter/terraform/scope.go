package terraform

import (
	"io/fs"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

// parsedFile pairs a filename with its parsed native-syntax body and raw source
// lines. The body drives evaluation; the lines let the scanner honor inline
// suppress annotations (imageref.IsIgnoredLine) by their source position.
type parsedFile struct {
	name  string
	body  *hclsyntax.Body
	lines []string
}

// parseTFFiles parses every .tf file in the directory. Files that fail to parse
// or are not native HCL syntax (e.g. .tf.json) are skipped rather than aborting
// the whole directory — a single malformed file must not blind the detector to
// its siblings.
func parseTFFiles(dir fs.FS, entries []fs.DirEntry) []parsedFile {
	parser := hclparse.NewParser()
	var files []parsedFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		content, err := fs.ReadFile(dir, e.Name())
		if err != nil {
			continue
		}
		f, diags := parser.ParseHCL(content, e.Name())
		if diags.HasErrors() {
			continue
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		files = append(files, parsedFile{
			name:  e.Name(),
			body:  body,
			lines: strings.Split(string(content), "\n"),
		})
	}
	return files
}

// buildEvalContext constructs the evaluation context used to resolve image
// expressions: a `var` object (variable defaults overridden by .tfvars) and a
// `local` object (locals evaluated to a fixpoint), plus the supported function
// set. Values that cannot be resolved are simply absent, so references to them
// error at evaluation time and the enclosing expression is skipped.
func buildEvalContext(dir fs.FS, entries []fs.DirEntry, files []parsedFile) *hcl.EvalContext {
	vars := collectVariables(files)
	applyTFVars(dir, entries, vars)
	locals := resolveLocals(files, vars)

	return &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"var":   objectOrEmpty(vars),
			"local": objectOrEmpty(locals),
		},
		Functions: tfFunctions,
	}
}

// baseContext is the context used to evaluate variable defaults and .tfvars
// values, which may reference functions but not var/local.
func baseContext() *hcl.EvalContext {
	return &hcl.EvalContext{Functions: tfFunctions}
}

// collectVariables evaluates the `default` value of every variable block across
// all files. Only the default attribute is read; type/description/validation are
// ignored (a `type` constraint is not an evaluable expression). Variables
// without a default are absent.
func collectVariables(files []parsedFile) map[string]cty.Value {
	vars := make(map[string]cty.Value)
	ctx := baseContext()
	for _, f := range files {
		for _, block := range f.body.Blocks {
			if block.Type != "variable" || len(block.Labels) != 1 {
				continue
			}
			def, ok := block.Body.Attributes["default"]
			if !ok {
				continue
			}
			v, diags := def.Expr.Value(ctx)
			if diags.HasErrors() || !v.IsWhollyKnown() {
				continue
			}
			vars[block.Labels[0]] = v
		}
	}
	return vars
}

// applyTFVars overlays .tfvars assignments onto vars, overriding any variable
// default with the same name. .tfvars files use native HCL syntax.
func applyTFVars(dir fs.FS, entries []fs.DirEntry, vars map[string]cty.Value) {
	parser := hclparse.NewParser()
	ctx := baseContext()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tfvars") {
			continue
		}
		content, err := fs.ReadFile(dir, e.Name())
		if err != nil {
			continue
		}
		f, diags := parser.ParseHCL(content, e.Name())
		if diags.HasErrors() {
			continue
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		for name, attr := range body.Attributes {
			v, diags := attr.Expr.Value(ctx)
			if diags.HasErrors() || !v.IsWhollyKnown() {
				continue
			}
			vars[name] = v
		}
	}
}

// resolveLocals evaluates every locals-block attribute to a fixpoint: each pass
// tries to resolve still-unresolved locals against the current context (var plus
// already-resolved locals); a pass that resolves nothing terminates the loop.
// Locals referencing resources, data sources or unknown functions never resolve
// and are left absent. Iteration is bounded by the number of locals.
func resolveLocals(files []parsedFile, vars map[string]cty.Value) map[string]cty.Value {
	type localExpr struct {
		name string
		expr hcl.Expression
	}
	var pending []localExpr
	for _, f := range files {
		for _, block := range f.body.Blocks {
			if block.Type != "locals" {
				continue
			}
			for name, attr := range block.Body.Attributes {
				pending = append(pending, localExpr{name: name, expr: attr.Expr})
			}
		}
	}

	resolved := make(map[string]cty.Value)
	for {
		progress := false
		var still []localExpr
		ctx := &hcl.EvalContext{
			Variables: map[string]cty.Value{
				"var":   objectOrEmpty(vars),
				"local": objectOrEmpty(resolved),
			},
			Functions: tfFunctions,
		}
		for _, l := range pending {
			v, diags := l.expr.Value(ctx)
			if diags.HasErrors() || !v.IsWhollyKnown() {
				still = append(still, l)
				continue
			}
			resolved[l.name] = v
			progress = true
		}
		pending = still
		if !progress || len(pending) == 0 {
			break
		}
	}
	return resolved
}

// objectOrEmpty wraps a value map as a cty object, returning an empty object for
// an empty map so that a reference to any attribute yields the desired "unknown
// attribute" error (which causes the enclosing expression to be skipped).
func objectOrEmpty(m map[string]cty.Value) cty.Value {
	if len(m) == 0 {
		return cty.EmptyObjectVal
	}
	return cty.ObjectVal(m)
}

// tfFunctions is the subset of Terraform's function set backed by go-cty's
// stdlib. It covers the string- and collection-shaping functions commonly used
// to assemble image references (format, join, replace, jsonencode, …).
// Terraform-only functions (templatefile, file, cidr*, …) are intentionally
// absent; expressions using them fail to evaluate and are skipped.
var tfFunctions = map[string]function.Function{
	"abs":          stdlib.AbsoluteFunc,
	"ceil":         stdlib.CeilFunc,
	"chomp":        stdlib.ChompFunc,
	"coalesce":     stdlib.CoalesceFunc,
	"coalescelist": stdlib.CoalesceListFunc,
	"concat":       stdlib.ConcatFunc,
	"contains":     stdlib.ContainsFunc,
	"distinct":     stdlib.DistinctFunc,
	"element":      stdlib.ElementFunc,
	"flatten":      stdlib.FlattenFunc,
	"floor":        stdlib.FloorFunc,
	"format":       stdlib.FormatFunc,
	"formatlist":   stdlib.FormatListFunc,
	"join":         stdlib.JoinFunc,
	"jsonencode":   stdlib.JSONEncodeFunc,
	"keys":         stdlib.KeysFunc,
	"length":       stdlib.LengthFunc,
	"lower":        stdlib.LowerFunc,
	"max":          stdlib.MaxFunc,
	"merge":        stdlib.MergeFunc,
	"min":          stdlib.MinFunc,
	"replace":      stdlib.ReplaceFunc,
	"reverse":      stdlib.ReverseListFunc,
	"slice":        stdlib.SliceFunc,
	"split":        stdlib.SplitFunc,
	"substr":       stdlib.SubstrFunc,
	"title":        stdlib.TitleFunc,
	"trim":         stdlib.TrimFunc,
	"trimprefix":   stdlib.TrimPrefixFunc,
	"trimspace":    stdlib.TrimSpaceFunc,
	"trimsuffix":   stdlib.TrimSuffixFunc,
	"upper":        stdlib.UpperFunc,
	"values":       stdlib.ValuesFunc,
}
