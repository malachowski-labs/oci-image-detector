package terraform

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/hcl/v2/ext/tryfunc"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

// tfFunctions is the subset of Terraform's function set available to the
// evaluator. It covers the string- and collection-shaping functions commonly
// used to assemble image references (format, join, replace, jsonencode, …).
// Most are go-cty's stdlib implementations; coalesce, lookup and replace are
// reimplemented below because Terraform's semantics differ from the stdlib's.
// Terraform-only functions (templatefile, file, cidr*, …) are intentionally
// absent; expressions using them fail to evaluate and only their literal
// sub-expressions are scanned.
//
// Every function here must have output bounded by the sum of its inputs, or be
// listed in amplifyingFuncs — the evaluation cost model depends on it.
var tfFunctions = map[string]function.Function{
	"abs":          stdlib.AbsoluteFunc,
	"can":          tryfunc.CanFunc,
	"ceil":         stdlib.CeilFunc,
	"chomp":        stdlib.ChompFunc,
	"coalesce":     coalesceFunc,
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
	"jsondecode":   stdlib.JSONDecodeFunc,
	"jsonencode":   stdlib.JSONEncodeFunc,
	"keys":         stdlib.KeysFunc,
	"length":       stdlib.LengthFunc,
	"lookup":       lookupFunc,
	"lower":        stdlib.LowerFunc,
	"max":          stdlib.MaxFunc,
	"merge":        stdlib.MergeFunc,
	"min":          stdlib.MinFunc,
	"regex":        stdlib.RegexFunc,
	"replace":      replaceFunc,
	"reverse":      stdlib.ReverseListFunc,
	"slice":        stdlib.SliceFunc,
	"split":        stdlib.SplitFunc,
	"substr":       stdlib.SubstrFunc,
	"title":        stdlib.TitleFunc,
	"tostring":     stdlib.MakeToFunc(cty.String),
	"trim":         stdlib.TrimFunc,
	"trimprefix":   stdlib.TrimPrefixFunc,
	"trimspace":    stdlib.TrimSpaceFunc,
	"trimsuffix":   stdlib.TrimSuffixFunc,
	"try":          tryfunc.TryFunc,
	"upper":        stdlib.UpperFunc,
	"values":       stdlib.ValuesFunc,
}

// amplifyingFuncs names the functions whose output can grow with the product
// of their first argument and the others, rather than the sum: format can
// reuse an argument per verb of its format string, join repeats its separator
// per element, replace repeats the replacement per match in its subject. The
// value is how many times the other arguments enter that product — formatlist
// additionally repeats the whole format once per list element.
var amplifyingFuncs = map[string]int{
	"format":     1,
	"formatlist": 2,
	"join":       1,
	"replace":    1,
}

// coalesceFunc mirrors Terraform's coalesce: it returns the first argument that
// is neither null nor an empty string. The stdlib variant only skips nulls.
var coalesceFunc = function.New(&function.Spec{
	VarParam: &function.Parameter{
		Name:      "vals",
		Type:      cty.DynamicPseudoType,
		AllowNull: true,
	},
	Type: func(args []cty.Value) (cty.Type, error) {
		types := make([]cty.Type, len(args))
		for i, a := range args {
			types[i] = a.Type()
		}
		ty, _ := convert.UnifyUnsafe(types)
		if ty == cty.NilType {
			return cty.NilType, errors.New("all arguments must have the same type")
		}
		return ty, nil
	},
	Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
		for _, a := range args {
			v, err := convert.Convert(a, retType)
			if err != nil {
				return cty.NilVal, err
			}
			if v.IsNull() || (retType == cty.String && v.AsString() == "") {
				continue
			}
			return v, nil
		}
		return cty.NilVal, errors.New("no non-null, non-empty-string arguments")
	},
})

// lookupFunc mirrors Terraform's lookup: it returns the map or object element
// for key, or the optional default when the key is absent. The stdlib variant
// requires the default.
var lookupFunc = function.New(&function.Spec{
	Params: []function.Parameter{
		{Name: "inputMap", Type: cty.DynamicPseudoType},
		{Name: "key", Type: cty.String},
	},
	VarParam: &function.Parameter{Name: "default", Type: cty.DynamicPseudoType, AllowNull: true},
	Type:     function.StaticReturnType(cty.DynamicPseudoType),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		if len(args) > 3 {
			return cty.NilVal, errors.New("lookup takes at most three arguments")
		}
		m, key := args[0], args[1].AsString()
		switch ty := m.Type(); {
		case ty.IsObjectType():
			if ty.HasAttribute(key) {
				return m.GetAttr(key), nil
			}
		case ty.IsMapType():
			if k := cty.StringVal(key); m.HasIndex(k).True() {
				return m.Index(k), nil
			}
		default:
			return cty.NilVal, errors.New("lookup requires a map or object")
		}
		if len(args) == 3 {
			return args[2], nil
		}
		return cty.NilVal, fmt.Errorf("lookup failed to find key %q", key)
	},
})

// replaceFunc mirrors Terraform's replace: a substring wrapped in forward
// slashes is treated as a regular expression, anything else as a literal. The
// stdlib variant is always literal.
var replaceFunc = function.New(&function.Spec{
	Params: []function.Parameter{
		{Name: "str", Type: cty.String},
		{Name: "substr", Type: cty.String},
		{Name: "replace", Type: cty.String},
	},
	Type: function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		str, substr, repl := args[0].AsString(), args[1].AsString(), args[2].AsString()
		if len(substr) > 1 && strings.HasPrefix(substr, "/") && strings.HasSuffix(substr, "/") {
			re, err := regexp.Compile(substr[1 : len(substr)-1])
			if err != nil {
				return cty.NilVal, err
			}
			return cty.StringVal(re.ReplaceAllString(str, repl)), nil
		}
		return cty.StringVal(strings.ReplaceAll(str, substr, repl)), nil
	},
})
