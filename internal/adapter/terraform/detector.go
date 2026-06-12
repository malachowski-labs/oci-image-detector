// Package terraform provides a DirectoryAwareDetector for Terraform files.
// It processes entire directories at once to enable cross-file variable
// resolution across .tf and .tfvars files.
package terraform

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/imageref"
	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// Strategy is the stable identifier for this detection strategy.
const Strategy domain.Strategy = "terraform"

var (
	// varBlockStartRe locates the opening of a variable block so we can
	// extract the full body with balanced-brace counting (see extractBalancedBlock).
	// Using [\w-]+ to support hyphenated HCL identifiers (e.g. "base-image").
	varBlockStartRe = regexp.MustCompile(`variable\s+"([\w-]+)"\s*\{`)

	// defaultValueRe extracts the default value from within an already-extracted
	// variable block body. Applied after balanced-brace extraction so nested
	// sub-blocks (validation {}, lifecycle {}) do not confuse the match.
	defaultValueRe = regexp.MustCompile(`\bdefault\s*=\s*"([^"]+)"`)

	// tfvarsAssignRe matches simple key = "value" assignments in .tfvars files.
	// Using [\w-]+ to support hyphenated variable names.
	tfvarsAssignRe = regexp.MustCompile(`(?m)^\s*([\w-]+)\s*=\s*"([^"]+)"`)

	// varRefRe matches a full var.name expression (the whole RHS is a var ref).
	// Using [\w-]+ to support hyphenated HCL identifiers.
	varRefRe = regexp.MustCompile(`^\s*var\.([\w-]+)\s*$`)

	// varInterpolRe matches ${var.name} inside a string.
	// Using [\w-]+ to support hyphenated HCL identifiers.
	varInterpolRe = regexp.MustCompile(`\$\{var\.([\w-]+)\}`)
)

// Detector implements port.DirectoryAwareDetector for Terraform directories.
type Detector struct{}

// New returns a new Terraform Detector.
func New() *Detector { return &Detector{} }

// Name implements port.DirectoryAwareDetector.
func (d *Detector) Name() string { return string(Strategy) }

// MatchDir implements port.DirectoryAwareDetector.
// Returns true when the directory contains at least one .tf file.
func (d *Detector) MatchDir(_ string, entries []fs.DirEntry) bool {
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tf") {
			return true
		}
	}
	return false
}

// DetectDir implements port.DirectoryAwareDetector.
// Two-pass approach:
//  1. Collect variable default values from all .tf and .tfvars files.
//  2. Find quoted strings in .tf files; try to resolve var references and
//     parse each candidate as an OCI image reference.
//
// The directory entries are read once and reused for both passes to avoid
// a redundant fs.ReadDir call.
func (d *Detector) DetectDir(dir fs.FS) ([]domain.Finding, error) {
	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		return nil, fmt.Errorf("terraform: read dir: %w", err)
	}

	// Pass 1: build variable defaults map.
	vars, err := collectVars(dir, entries)
	if err != nil {
		return nil, err
	}

	// Pass 2: scan .tf files for image refs.
	var findings []domain.Finding
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		content, err := fs.ReadFile(dir, e.Name())
		if err != nil {
			return findings, fmt.Errorf("terraform: read %q: %w", e.Name(), err)
		}
		findings = append(findings, scanTFFile(e.Name(), content, vars)...)
	}

	return findings, nil
}

// collectVars builds a map of variable name → value from all .tf and .tfvars
// files in the directory. Two-pass: .tf defaults first, .tfvars second so that
// explicit tfvars assignments always override variable block defaults.
// entries must be the pre-read slice from the parent fs.ReadDir call.
func collectVars(dir fs.FS, entries []fs.DirEntry) (map[string]string, error) {
	vars := make(map[string]string)

	// Pass 1: variable defaults from .tf files.
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		content, err := fs.ReadFile(dir, e.Name())
		if err != nil {
			return nil, fmt.Errorf("terraform: read vars from %q: %w", e.Name(), err)
		}
		extractVarDefaults(string(content), vars)
	}

	// Pass 2: .tfvars assignments override defaults.
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tfvars") {
			continue
		}
		content, err := fs.ReadFile(dir, e.Name())
		if err != nil {
			return nil, fmt.Errorf("terraform: read vars from %q: %w", e.Name(), err)
		}
		extractTFVars(string(content), vars)
	}

	return vars, nil
}

// extractVarDefaults populates vars from variable blocks in a .tf file.
// It uses balanced-brace counting (extractBalancedBlock) to correctly handle
// variable blocks that contain nested sub-blocks such as validation {} or
// lifecycle {}, which would confuse a regex-only approach.
func extractVarDefaults(content string, vars map[string]string) {
	for _, loc := range varBlockStartRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[loc[2]:loc[3]]
		// varBlockStartRe ends with `\{`; loc[1]-1 is the byte index of that `{`.
		body := extractBalancedBlock(content, loc[1]-1)
		if body == "" {
			continue
		}
		if m := defaultValueRe.FindStringSubmatch(body); m != nil {
			vars[name] = m[1]
		}
	}
}

// extractBalancedBlock returns the substring of s starting at the opening
// brace at position start through the matching closing brace (inclusive).
// Returns "" if the braces are unbalanced or start does not point to '{'.
func extractBalancedBlock(s string, start int) string {
	if start >= len(s) || s[start] != '{' {
		return ""
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return "" // unbalanced
}

// extractTFVars populates vars from key = "value" assignments in a .tfvars file.
func extractTFVars(content string, vars map[string]string) {
	for _, m := range tfvarsAssignRe.FindAllStringSubmatch(content, -1) {
		vars[m[1]] = m[2]
	}
}

// scanTFFile extracts image ref findings from a single .tf file.
//
// Candidates are identified with imageref.Candidates — the same strict
// registry-host + path + tag/digest filter the generic detector uses — rather
// than treating every quoted string as a potential image. This keeps the
// detector from emitting IAM members, GCP resource paths, module sources and
// other quoted strings that merely contain "/" or ":". Variable and
// interpolation references are resolved first so the filter sees concrete
// values; references that resolve to a bare form (e.g. "nginx:1.25") or that
// cannot be resolved are intentionally not reported, matching the generic
// detector's precision contract.
func scanTFFile(filename string, content []byte, vars map[string]string) []domain.Finding {
	lines := strings.Split(string(content), "\n")
	var findings []domain.Finding

	for lineIdx, line := range lines {
		lineNum := uint(lineIdx + 1)

		// Resolve ${var.name} interpolations so the filter sees concrete values.
		resolved := varInterpolRe.ReplaceAllStringFunc(line, func(match string) string {
			sub := varInterpolRe.FindStringSubmatch(match)
			if val, ok := vars[sub[1]]; ok {
				return val
			}
			return match // unresolvable — keep placeholder
		})

		candidates := imageref.Candidates(resolved)

		// Case: bare `<lhs> = var.name` reference (the RHS is exactly a
		// variable). Scan the resolved value for candidates too.
		if m := varRefRe.FindStringSubmatch(afterEquals(line)); m != nil {
			if val, ok := vars[m[1]]; ok {
				candidates = append(candidates, imageref.Candidates(val)...)
			}
		}

		for _, raw := range candidates {
			ref := imageref.Parse(raw)
			if !imageref.LooksLikeImage(ref) {
				continue
			}
			findings = append(findings, domain.Finding{
				Ref:      ref,
				FilePath: filename,
				Line:     lineNum,
				Strategy: Strategy,
			})
		}
	}

	return findings
}

// afterEquals returns the portion of a line after the first "=" character,
// used to check bare variable references like: image = var.name
func afterEquals(line string) string {
	if i := strings.IndexByte(line, '='); i >= 0 {
		return line[i+1:]
	}
	return ""
}
