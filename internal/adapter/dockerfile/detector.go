// Package dockerfile provides a Detector for Dockerfile files.
package dockerfile

import (
	"bufio"
	"bytes"
	"path"
	"regexp"
	"strings"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/imageref"
	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// Strategy is the stable identifier for this detection strategy.
const Strategy domain.Strategy = "dockerfile"

// maxScanTokenSize is the maximum line length the scanner will handle.
// The bufio default of 64 KiB is raised to 1 MiB to avoid silent truncation
// on unusually wide lines (e.g. inline heredocs).
const maxScanTokenSize = 1 << 20 // 1 MiB

// argRefRe matches a build-arg reference in either ${NAME} or $NAME form.
// Modifier syntax such as ${NAME:-default} is intentionally not expanded; such
// references are left untouched and surface as raw template findings.
var argRefRe = regexp.MustCompile(`\$\{([a-zA-Z0-9_]+)\}|\$([a-zA-Z0-9_]+)`)

// Detector implements port.Detector for Dockerfile files.
type Detector struct{}

// New returns a new Dockerfile Detector.
func New() *Detector { return &Detector{} }

// Name implements port.Detector.
func (d *Detector) Name() string { return string(Strategy) }

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
type instruction struct {
	line uint
	text string
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
func (d *Detector) Detect(filePath string, content []byte) ([]domain.Finding, error) {
	// On a scanner error logicalInstructions returns the instructions parsed so
	// far together with the error; we still emit findings for that partial input
	// and surface the error to the caller (the scan service logs it).
	instrs, err := logicalInstructions(content)

	args := map[string]string{} // build-arg defaults, by name
	stages := map[string]bool{} // declared stage names, lower-cased
	var findings []domain.Finding

	for _, in := range instrs {
		if imageref.IsIgnoredLine(in.text) {
			continue
		}
		fields := strings.Fields(in.text)
		if len(fields) == 0 {
			continue
		}

		switch strings.ToUpper(fields[0]) {
		case "ARG":
			collectArgs(fields[1:], args)

		case "FROM":
			raw, alias, ok := parseFrom(fields[1:])
			if !ok {
				continue
			}
			raw = expandArgs(raw, args)
			if alias != "" {
				stages[strings.ToLower(alias)] = true
			}
			// scratch is the empty image; a stage reference is an internal name.
			if strings.EqualFold(raw, "scratch") || stages[strings.ToLower(raw)] {
				continue
			}
			findings = append(findings, newFinding(raw, in.line, filePath))

		case "COPY":
			raw, ok := copyFromImage(fields[1:], args, stages)
			if !ok {
				continue
			}
			findings = append(findings, newFinding(raw, in.line, filePath))
		}
	}

	return findings, err
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
func logicalInstructions(content []byte) ([]instruction, error) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, maxScanTokenSize), maxScanTokenSize)

	var (
		out        []instruction
		cur        strings.Builder
		startLine  uint
		lineNum    uint
		continuing bool
	)

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Comment lines are dropped whether or not we are mid-continuation.
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !continuing {
			if trimmed == "" {
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
		out = append(out, instruction{line: startLine, text: strings.TrimSpace(cur.String())})
		continuing = false
	}

	// A file ending on a continuation still yields its accumulated instruction.
	if continuing {
		out = append(out, instruction{line: startLine, text: strings.TrimSpace(cur.String())})
	}

	return out, scanner.Err()
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
func copyFromImage(fields []string, args map[string]string, stages map[string]bool) (string, bool) {
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

	from = expandArgs(from, args)
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

// expandArgs substitutes ${NAME}/$NAME references with recorded ARG values.
// Unknown references are left intact.
func expandArgs(s string, args map[string]string) string {
	return argRefRe.ReplaceAllStringFunc(s, func(match string) string {
		m := argRefRe.FindStringSubmatch(match)
		name := m[1] // ${NAME} form
		if name == "" {
			name = m[2] // $NAME form
		}
		if val, ok := args[name]; ok {
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

// isAllDigits reports whether s is non-empty and consists only of ASCII digits.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
