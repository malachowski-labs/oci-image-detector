// Package generic provides a regex-based fallback Detector for files not
// handled by any specialist detector.
package generic

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
const Strategy domain.Strategy = "generic"

// maxScanTokenSize is the maximum line length the scanner will handle.
// The bufio default of 64 KiB is raised to 1 MiB to avoid silent truncation
// on minified JSON, auto-generated YAML, or other wide-line files.
const maxScanTokenSize = 1 << 20 // 1 MiB

// candidateRe matches strings that are likely OCI image references.
//
// The generic detector is the fallback for arbitrary files (prose, JSON, YAML,
// scripts), where almost any "word/word" or "word:word" token would otherwise
// look like an image. To keep precision high it matches only fully-qualified
// references and deliberately ignores ambiguous short forms — bare library
// images ("nginx:1.25"), single-namespace paths ("org/repo"), fractions
// ("0/2"), file paths ("docs/overview.md") and config pairs ("language:go").
// Those short forms are the job of the specialist detectors, which have file
// format context (a Dockerfile FROM line, a Helm/Kubernetes "image:" key) to
// resolve them safely.
//
// A match therefore requires both:
//
//  1. An identifiable registry host — a dotted domain (ghcr.io, gcr.io,
//     123.dkr.ecr.us-east-1.amazonaws.com) or an explicit host:port
//     (localhost:5000). A plain word is never treated as a registry.
//  2. A mandatory tag or digest. The tag is anchored to end on an alphanumeric
//     so trailing sentence punctuation is not captured (RE2 has no lookahead),
//     e.g. "deploy ghcr.io/org/app:v1." yields "ghcr.io/org/app:v1".
var candidateRe = regexp.MustCompile(
	`(?:` +
		// registry host: dotted domain (optional :port) or host:port.
		`(?:` +
		`[a-z0-9]+(?:[.-][a-z0-9]+)*\.[a-z0-9]+(?:[.-][a-z0-9]+)*(?::[0-9]+)?` +
		`|[a-z0-9]+(?:[.-][a-z0-9]+)*:[0-9]+` +
		`)` +
		// repository path: one or more "/component" segments.
		`(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+` +
		// mandatory tag (optionally followed by a digest) or a bare digest.
		`(?::[a-zA-Z0-9](?:[a-zA-Z0-9._-]*[a-zA-Z0-9])?(?:@sha256:[a-fA-F0-9]+)?` +
		`|@sha256:[a-fA-F0-9]+)` +
		`)`,
)

// specialistExtensions lists file extensions exclusively owned by specialist
// detectors. Generic.Match returns false for these.
var specialistExtensions = []string{".tf", ".tfvars"}

// Detector implements port.Detector as a regex-based fallback.
type Detector struct{}

// New returns a new Generic Detector.
func New() *Detector { return &Detector{} }

// Name implements port.Detector.
func (d *Detector) Name() string { return string(Strategy) }

// Match implements port.Detector.
// Returns true for any file not owned by a specialist detector.
func (d *Detector) Match(filePath string) bool {
	lower := strings.ToLower(path.Base(filePath))

	// Dockerfile variants — owned by the dockerfile detector.
	if lower == "dockerfile" ||
		strings.HasPrefix(lower, "dockerfile.") ||
		strings.HasSuffix(lower, ".dockerfile") {
		return false
	}

	// Extension-based specialist files (.tf, .tfvars).
	for _, ext := range specialistExtensions {
		if strings.HasSuffix(lower, ext) {
			return false
		}
	}

	// Helm values files.
	if lower == "values.yaml" || lower == "values.yml" {
		return false
	}

	return true
}

// Detect implements port.Detector.
// Scans each line for candidate image reference patterns and validates them
// with go-containerregistry.
func (d *Detector) Detect(filePath string, content []byte) ([]domain.Finding, error) {
	var findings []domain.Finding
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, maxScanTokenSize), maxScanTokenSize)
	var lineNum uint

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		for _, match := range candidateRe.FindAllString(line, -1) {
			ref := imageref.Parse(match)
			if !imageref.LooksLikeImage(ref) {
				continue
			}
			findings = append(findings, domain.Finding{
				Ref:      ref,
				FilePath: filePath,
				Line:     lineNum,
				Strategy: Strategy,
			})
		}
	}

	return findings, scanner.Err()
}
