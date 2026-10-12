package terraform

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/imageref"
)

// Evaluation limits. The detector runs on untrusted input, and a few bytes of
// HCL can describe an arbitrarily large value (locals that concatenate each
// other, nested for expressions), so every evaluation is priced before it runs.
const (
	// evalBudget is the total estimated cost a single directory may spend on
	// evaluation. See scope.cost for the unit.
	evalBudget int64 = 16 << 20

	// maxValueBytes caps the size of any single evaluated value.
	maxValueBytes = 1 << 20
)

// errLimited is returned by DetectDir alongside the findings when a limit above
// caused some expressions to be left unresolved.
var errLimited = errors.New("terraform: evaluation limits reached; some expressions were not resolved")

// parsedFile is one .tf file. body is nil when nothing could be parsed; broken
// marks a file with syntax errors, whose body is only a partial recovery. The
// raw lines let the scanner honor inline suppress annotations
// (imageref.IsIgnoredLine) by source position.
type parsedFile struct {
	name   string
	body   *hclsyntax.Body
	lines  []string
	broken bool
}

// parseFile reads and parses a native-syntax HCL file. A syntax error is
// reported but does not discard the file: the parser recovers what it can, and
// the returned file is flagged broken so the caller can compensate.
func parseFile(dir fs.FS, name string) (parsedFile, error) {
	content, err := fs.ReadFile(dir, name)
	if err != nil {
		return parsedFile{}, fmt.Errorf("terraform: read %q: %w", name, err)
	}
	file := parsedFile{name: name, lines: strings.Split(string(content), "\n")}

	f, diags := hclsyntax.ParseConfig(content, name, hcl.InitialPos)
	if f != nil {
		file.body, _ = f.Body.(*hclsyntax.Body)
	}
	if diags.HasErrors() {
		file.broken = true
		return file, fmt.Errorf("terraform: parse %q: %w", name, diags)
	}
	return file, nil
}

// parseTFFiles parses every .tf file in the directory. A file that cannot be
// read is skipped and a malformed one is kept as broken; either way the error
// is collected rather than aborting — a single bad file must not blind the
// detector to its siblings.
func parseTFFiles(dir fs.FS, entries []fs.DirEntry) ([]parsedFile, []error) {
	var (
		files []parsedFile
		errs  []error
	)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		file, err := parseFile(dir, e.Name())
		if err != nil {
			errs = append(errs, err)
		}
		if file.lines != nil {
			files = append(files, file)
		}
	}
	return files, errs
}

// spansIgnoredLine reports whether any source line covered by rng carries the
// inline suppress annotation.
func spansIgnoredLine(lines []string, rng hcl.Range) bool {
	for l := rng.Start.Line; l <= rng.End.Line; l++ {
		if isIgnoredLine(lines, l) {
			return true
		}
	}
	return false
}

// isIgnoredLine reports whether the 1-based source line carries the inline
// suppress annotation.
func isIgnoredLine(lines []string, line int) bool {
	return line >= 1 && line <= len(lines) && imageref.IsIgnoredLine(lines[line-1])
}

// sized is an evaluated value together with its measured footprint.
type sized struct {
	val   cty.Value
	bytes int64
	nodes int64
}

// local is a locals-block attribute, evaluated on first reference.
type local struct {
	expr      hcl.Expression
	value     sized
	ok        bool
	resolving bool
	done      bool
}

// scope holds the values image expressions are resolved against: variables
// (defaults overridden by .tfvars) and locals. Locals are evaluated lazily, in
// dependency order, the first time an expression references them.
//
// A definition annotated with the inline suppress marker is withheld from the
// scope, so the suppressed value is not reported again wherever it is used.
type scope struct {
	vars   map[string]sized
	locals map[string]*local

	budget  int64
	limited bool
}

// newScope builds the scope for a directory from its parsed .tf files and its
// .tfvars files. Errors reading or parsing .tfvars files are returned for
// reporting; the scope is usable regardless.
func newScope(dir fs.FS, entries []fs.DirEntry, files []parsedFile) (*scope, []error) {
	sc := &scope{
		vars:   make(map[string]sized),
		locals: make(map[string]*local),
		budget: evalBudget,
	}

	for _, f := range files {
		if f.body == nil {
			continue
		}
		for _, block := range f.body.Blocks {
			switch {
			case block.Type == "variable" && len(block.Labels) == 1:
				// Only the default is read; type/description/validation are
				// not evaluable expressions.
				if def, ok := block.Body.Attributes["default"]; ok {
					sc.setVar(block.Labels[0], def, f.lines)
				}
			case block.Type == "locals":
				for name, attr := range block.Body.Attributes {
					if !spansIgnoredLine(f.lines, attr.SrcRange) {
						sc.locals[name] = &local{expr: attr.Expr}
					}
				}
			}
		}
	}

	// .tfvars assignments override variable defaults. Files are applied in
	// directory order, so a later file wins over an earlier one.
	var errs []error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tfvars") {
			continue
		}
		f, err := parseFile(dir, e.Name())
		if err != nil {
			errs = append(errs, err)
		}
		if f.body == nil {
			continue
		}
		for name, attr := range f.body.Attributes {
			sc.setVar(name, attr, f.lines)
		}
	}
	return sc, errs
}

// setVar records a variable value from a default or .tfvars attribute. A
// suppressed assignment withholds the variable altogether; one that cannot be
// resolved leaves any earlier value in place.
func (sc *scope) setVar(name string, attr *hclsyntax.Attribute, lines []string) {
	if spansIgnoredLine(lines, attr.SrcRange) {
		delete(sc.vars, name)
		return
	}
	if v, ok := sc.eval(attr.Expr); ok {
		sc.vars[name] = v
	}
}

// local returns the value of the named local, evaluating it on first use.
// A local that is undefined, unresolvable or part of a reference cycle reports
// false.
func (sc *scope) local(name string) (sized, bool) {
	l, ok := sc.locals[name]
	if !ok || l.resolving {
		return sized{}, false
	}
	if !l.done {
		l.resolving = true
		l.value, l.ok = sc.eval(l.expr)
		l.resolving, l.done = false, true
	}
	return l.value, l.ok
}

// lookup resolves a `var.<name>` or `local.<name>` traversal to its value.
func (sc *scope) lookup(t hcl.Traversal) (root, name string, v sized, ok bool) {
	if len(t) < 2 {
		return "", "", sized{}, false
	}
	attr, isAttr := t[1].(hcl.TraverseAttr)
	if !isAttr {
		return "", "", sized{}, false
	}
	switch root, name = t.RootName(), attr.Name; root {
	case "var":
		v, ok = sc.vars[name]
	case "local":
		v, ok = sc.local(name)
	}
	return root, name, v, ok
}

// eval evaluates expr against the scope. It reports false when the expression
// cannot be fully resolved statically (it references resources, data sources,
// unknown functions or absent values) or when evaluating it would exceed the
// evaluation limits.
func (sc *scope) eval(expr hcl.Expression) (sized, bool) {
	// Resolve the referenced values first: this evaluates any locals the
	// expression depends on, so their sizes are known when pricing it.
	refs := map[string]map[string]cty.Value{"var": {}, "local": {}}
	for _, t := range expr.Variables() {
		if root, name, v, ok := sc.lookup(t); ok {
			refs[root][name] = v.val
		}
	}

	cost := sc.cost(expr)
	if cost > sc.budget {
		sc.limited = true
		return sized{}, false
	}
	sc.budget -= cost

	val, diags := expr.Value(&hcl.EvalContext{
		Variables: map[string]cty.Value{
			"var":   cty.ObjectVal(refs["var"]),
			"local": cty.ObjectVal(refs["local"]),
		},
		Functions: tfFunctions,
	})
	if diags.HasErrors() || !val.IsWhollyKnown() {
		return sized{}, false
	}
	v := measure(val)
	if v.bytes > maxValueBytes {
		sc.limited = true
		return sized{}, false
	}
	return v, true
}

// measure returns v with its footprint: the bytes of every string it contains
// plus one per value node, and the node count on its own.
func measure(v cty.Value) sized {
	s := sized{val: v, bytes: 1, nodes: 1}
	if v.IsNull() || !v.IsKnown() {
		return s
	}
	switch ty := v.Type(); {
	case ty == cty.String:
		s.bytes += int64(len(v.AsString()))
	case ty.IsCollectionType() || ty.IsTupleType() || ty.IsObjectType():
		for it := v.ElementIterator(); it.Next(); {
			k, ev := it.Element()
			for _, child := range []sized{measure(k), measure(ev)} {
				s.bytes += child.bytes
				s.nodes += child.nodes
			}
		}
	}
	return s
}

// costCap saturates cost arithmetic well above any budget and well below
// overflow.
const costCap int64 = 1 << 40

// cost returns an upper bound on the work of evaluating expr, in units of
// roughly one byte produced or one value visited. Sub-expression costs add up,
// except where evaluation can multiply them: a for expression repeats its body
// once per element, and an amplifying function can repeat its other arguments
// once per unit of its first.
func (sc *scope) cost(expr hcl.Expression) int64 {
	node, ok := expr.(hclsyntax.Node)
	if !ok {
		return costCap
	}
	w := &costWalker{sc: sc, stack: []costFrame{{}}}
	_ = hclsyntax.Walk(node, w)
	return w.stack[0].first
}

// costFrame accumulates the costs of one node's children: the first child on
// its own (a for expression's collection, a function's first argument) and the
// sum of the others.
type costFrame struct {
	children int
	first    int64
	rest     int64
}

// costWalker computes scope.cost bottom-up over the syntax tree.
type costWalker struct {
	sc    *scope
	stack []costFrame
}

func (w *costWalker) Enter(hclsyntax.Node) hcl.Diagnostics {
	w.stack = append(w.stack, costFrame{})
	return nil
}

func (w *costWalker) Exit(node hclsyntax.Node) hcl.Diagnostics {
	children := w.stack[len(w.stack)-1]
	w.stack = w.stack[:len(w.stack)-1]

	first, rest := children.first, children.rest
	cost := addCost(first, rest)
	switch n := node.(type) {
	case *hclsyntax.LiteralValueExpr:
		cost = measure(n.Val).bytes
	case *hclsyntax.ScopeTraversalExpr:
		if _, _, v, ok := w.sc.lookup(n.Traversal); ok {
			cost = v.bytes
		}
	case *hclsyntax.ForExpr:
		// The collection is the first child. The body runs once per element,
		// and each use of the iterator in it can copy part of the collection.
		elements := w.elements(n.CollExpr, first)
		cost = addCost(first, mulCost(rest, addCost(first, elements)))
	case *hclsyntax.FunctionCallExpr:
		product := first
		for range amplifyingFuncs[n.Name] {
			product = mulCost(product, rest)
			cost = addCost(first, product)
		}
	}
	cost = addCost(cost, 1)

	parent := &w.stack[len(w.stack)-1]
	if parent.children == 0 {
		parent.first = cost
	} else {
		parent.rest = addCost(parent.rest, cost)
	}
	parent.children++
	return nil
}

// elements bounds the number of elements coll yields when iterated. When the
// collection's shape is not evident from the syntax, fallback is used.
func (w *costWalker) elements(coll hclsyntax.Expression, fallback int64) int64 {
	switch c := coll.(type) {
	case *hclsyntax.TupleConsExpr:
		return int64(len(c.Exprs))
	case *hclsyntax.ObjectConsExpr:
		return int64(len(c.Items))
	case *hclsyntax.ScopeTraversalExpr:
		if _, _, v, ok := w.sc.lookup(c.Traversal); ok {
			return v.nodes
		}
	}
	return fallback
}

func addCost(a, b int64) int64 {
	return min(a+b, costCap)
}

func mulCost(a, b int64) int64 {
	if a != 0 && b > costCap/a {
		return costCap
	}
	return min(a*b, costCap)
}
