// Package dockerfile provides a Detector for Dockerfile files.
package dockerfile

import (
	"bytes"
	"path"
	"regexp"
	"strings"

	"github.com/malachowski-labs/oci-image-detector/pkg/domain"
	"github.com/malachowski-labs/oci-image-detector/pkg/imageref"
	"github.com/malachowski-labs/oci-image-detector/pkg/port"
	"github.com/malachowski-labs/oci-image-detector/pkg/registry"
	"github.com/malachowski-labs/oci-image-detector/pkg/suppress"
)

// Strategy is the stable identifier for this detection strategy.
const Strategy domain.Strategy = "dockerfile"

// argRefRe matches a build-arg reference in either ${NAME} or $NAME form.
// Modifier syntax such as ${NAME:-default} is intentionally not expanded; such
// references are left untouched and surface as raw template findings.
var argRefRe = regexp.MustCompile(`\$\{([a-zA-Z0-9_]+)\}|\$([a-zA-Z0-9_]+)`)

// Detector implements port.Detector for Dockerfile files.
type Detector struct{}

// init makes the detector available to every binary that links this package.
func init() { registry.MustRegister(&Detector{}) }

// Name implements port.Detector.
func (d *Detector) Name() string { return string(Strategy) }

// Tier implements port.Detector.
func (d *Detector) Tier() port.Tier { return port.TierSpecialist }

// Match implements port.Detector.
// Returns true for: Dockerfile, Dockerfile.<suffix>, *.<suffix>dockerfile (case-insensitive).
func (d *Detector) Match(filePath string) bool {
	lower := strings.ToLower(path.Base(filePath))
	return lower == "dockerfile" ||
		strings.HasPrefix(lower, "dockerfile.") ||
		strings.HasSuffix(lower, ".dockerfile")
}

// instruction is a single logical Dockerfile instruction with the 1-based line
// number on which it begins. Physical lines joined by a trailing backslash
// collapse into one instruction carrying the line of the first physical line.
// ignored is set when a comment line directly above the instruction, or inside
// its continuation, carries the inline suppression annotation.
type instruction struct {
	line    uint
	text    string
	ignored bool
}

// Detect implements port.Detector.
//
// It reports the base image of every FROM instruction and every external image
// referenced via COPY --from. It is deliberately not a full Dockerfile frontend
// (see docs/adr/0001); it handles the constructs that carry image references:
//
//   - line continuations (a trailing "\" joins physical lines);
//   - ARG defaults expanded into later FROM/COPY references (all declared
//     defaults are tracked; we do not model Docker's global-vs-stage ARG scope,
//     which only ever resolves more references, never fewer);
//   - stage aliases ("FROM x AS build") — references to a known stage name, by
//     FROM or by COPY --from, are not images and are skipped;
//   - "scratch", which is the empty base image and never a real reference.
//
// The text of every other instruction (RUN, ENV, LABEL, …) is scanned with the
// strict imageref.Candidates filter, so fully-qualified references in it are
// still found. An ARG default is reported where a FROM or COPY --from uses
// it; one that neither of them uses is scanned the same way.
func (d *Detector) Detect(filePath string, content []byte) ([]domain.Finding, error) {
	args := map[string]string{} // build-arg defaults, by name
	used := map[string]bool{}   // build args expanded into a FROM or COPY --from
	stages := map[string]bool{} // declared stage names, lower-cased
	var argLines []instruction  // ARG instructions, in file order
	var findings []domain.Finding

	for _, in := range logicalInstructions(content) {
		// The annotation in the instruction text itself is honoured too: after
		// a RUN it is a shell comment, so it does not break the build.
		if in.ignored || suppress.IsIgnoredLine(in.text) {
			continue
		}
		fields := strings.Fields(in.text)
		if len(fields) == 0 {
			continue
		}

		switch strings.ToUpper(fields[0]) {
		case "ARG":
			collectArgs(fields[1:], args)
			argLines = append(argLines, in)

		case "FROM":
			raw, alias, ok := parseFrom(fields[1:])
			if !ok {
				continue
			}
			raw = expandArgs(raw, args, used)
			if alias != "" {
				stages[strings.ToLower(alias)] = true
			}
			// scratch is the empty image; a stage reference is an internal name.
			if strings.EqualFold(raw, "scratch") || stages[strings.ToLower(raw)] {
				continue
			}
			findings = append(findings, newFinding(raw, in.line, filePath))

		case "COPY":
			raw, ok := copyFromImage(fields[1:], args, used, stages)
			if !ok {
				continue
			}
			findings = append(findings, newFinding(raw, in.line, filePath))

		default:
			findings = append(findings, textFindings(in.text, in.line, filePath)...)
		}
	}

	return append(findings, unusedDefaults(argLines, used, filePath)...), nil
}

// textFindings reports the fully-qualified references in text that no
// instruction marks as an image, as selected by the strict
// imageref.Candidates filter and not rejected by imageref.IsSourceFile.
func textFindings(text string, line uint, filePath string) []domain.Finding {
	var findings []domain.Finding
	for _, raw := range imageref.Candidates(text) {
		if f := newFinding(raw, line, filePath); !imageref.IsSourceFile(f.Ref) {
			findings = append(findings, f)
		}
	}
	return findings
}

// unusedDefaults reports the fully-qualified references among the ARG
// defaults that no FROM or COPY --from expanded. A default that one of them
// uses is reported there instead, so it is not reported twice.
func unusedDefaults(argLines []instruction, used map[string]bool, filePath string) []domain.Finding {
	var findings []domain.Finding
	for _, in := range argLines {
		for _, f := range strings.Fields(in.text)[1:] {
			name, val, ok := strings.Cut(f, "=")
			if ok && !used[name] {
				findings = append(findings, textFindings(val, in.line, filePath)...)
			}
		}
	}
	return findings
}

// newFinding builds a Finding for a raw image reference at the given location.
func newFinding(raw string, line uint, filePath string) domain.Finding {
	return domain.Finding{
		Ref:      imageref.Parse(raw),
		FilePath: filePath,
		Line:     line,
		Strategy: Strategy,
	}
}

// logicalInstructions splits content into logical instructions, joining
// backslash-continued physical lines and dropping blank and comment lines
// (including comment lines that appear within a continuation, which Docker
// strips). Each instruction keeps the line number of its first physical line.
//
// A dropped comment line may still carry the inline suppression annotation,
// since a comment of its own is the only place Docker accepts one on every
// instruction. It marks the instruction it sits in, or the one that follows
// its comment block; a blank line detaches the block from that instruction.
func logicalInstructions(content []byte) []instruction {
	var (
		out        []instruction
		cur        strings.Builder
		startLine  uint
		lineNum    uint
		continuing bool
		ignored    bool
	)

	for raw := range bytes.Lines(content) {
		lineNum++
		line := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
		trimmed := strings.TrimSpace(line)

		// Comment lines are dropped whether or not we are mid-continuation.
		if strings.HasPrefix(trimmed, "#") {
			ignored = ignored || suppress.IsIgnoredLine(trimmed)
			continue
		}
		if !continuing {
			if trimmed == "" {
				ignored = false
				continue
			}
			startLine = lineNum
			cur.Reset()
		}

		// A trailing backslash continues the instruction on the next line.
		if body, ok := strings.CutSuffix(strings.TrimRight(line, " \t"), `\`); ok {
			cur.WriteString(body)
			cur.WriteByte(' ')
			continuing = true
			continue
		}

		cur.WriteString(line)
		out = append(out, instruction{line: startLine, text: strings.TrimSpace(cur.String()), ignored: ignored})
		continuing = false
		ignored = false
	}

	// A file ending on a continuation still yields its accumulated instruction.
	if continuing {
		out = append(out, instruction{line: startLine, text: strings.TrimSpace(cur.String()), ignored: ignored})
	}

	return out
}

// parseFrom extracts the image reference and optional stage alias from the
// arguments of a FROM instruction (the fields after "FROM").
// Handles: FROM [--platform=<p>] <image> [AS <name>]
func parseFrom(fields []string) (image, alias string, ok bool) {
	i := 0
	for i < len(fields) && strings.HasPrefix(fields[i], "--") {
		i++
	}
	if i >= len(fields) {
		return "", "", false
	}
	image = fields[i]
	i++
	if i+1 < len(fields) && strings.EqualFold(fields[i], "AS") {
		alias = fields[i+1]
	}
	return image, alias, true
}

// copyFromImage returns the external image referenced by a COPY --from flag, if
// any. Stage references (by name or numeric index) and unresolved templates are
// not images and yield ok=false.
func copyFromImage(fields []string, args map[string]string, used, stages map[string]bool) (string, bool) {
	from := ""
	found := false
	for _, f := range fields {
		if rest, ok := strings.CutPrefix(f, "--from="); ok {
			from = rest
			found = true
			break
		}
	}
	if !found {
		return "", false
	}

	from = expandArgs(from, args, used)
	if from == "" || strings.Contains(from, "$") {
		return "", false // unresolved template — too ambiguous to report
	}
	if isAllDigits(from) || stages[strings.ToLower(from)] {
		return "", false // numeric or named stage reference, not an image
	}
	return from, true
}

// collectArgs records build-arg defaults (NAME=value) from an ARG instruction.
// ARGs without a default carry no value and are not recorded, so references to
// them remain unresolved (and surface as raw template findings).
func collectArgs(fields []string, args map[string]string) {
	for _, f := range fields {
		if strings.HasPrefix(f, "--") {
			continue
		}
		name, val, ok := strings.Cut(f, "=")
		if ok {
			args[name] = unquote(val)
		}
	}
}

// expandArgs substitutes ${NAME}/$NAME references with recorded ARG values and
// records the substituted names in used. Unknown references are left intact.
func expandArgs(s string, args map[string]string, used map[string]bool) string {
	return argRefRe.ReplaceAllStringFunc(s, func(match string) string {
		m := argRefRe.FindStringSubmatch(match)
		name := m[1] // ${NAME} form
		if name == "" {
			name = m[2] // $NAME form
		}
		if val, ok := args[name]; ok {
			used[name] = true
			return val
		}
		return match
	})
}

// unquote strips a single pair of matching surrounding quotes.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// isAllDigits reports whether s consists only of ASCII digits. Callers
// must pass a non-empty string.
func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
