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
// Three alternatives, in order of specificity:
//
//  1. registry/repo[:tag][@digest] — any host-qualified path (contains a host
//     component with dots/port before the first slash).
//  2. org/repo:tag — two-component Docker Hub path with an explicit tag.
//  3. name:tag — bare Docker Hub official image (no registry or namespace
//     prefix). Requires the name to be at least two lowercase chars and the
//     tag to be at least two chars so that common "key:value" pairs from
//     config files are not matched. This alternative intentionally accepts
//     some false positives (e.g. "http:path") in exchange for catching the
//     most common library images (nginx:1.25, redis:alpine, postgres:14).
//     False positives are further reduced by imageref.LooksLikeImage.
var candidateRe = regexp.MustCompile(
	`(?:` +
		// 1. registry/repo[:tag][@digest]
		`[a-zA-Z0-9][a-zA-Z0-9._-]*(?::[0-9]+)?/[a-z0-9][a-z0-9._/-]*` +
		`(?::[a-zA-Z0-9._-]+)?(?:@sha256:[a-fA-F0-9]+)?` +
		`|` +
		// 2. org/repo:tag
		`[a-z0-9][a-z0-9._-]*/[a-z0-9][a-z0-9._/-]*:[a-zA-Z0-9._-]+` +
		`|` +
		// 3. name:tag (bare Docker Hub library image)
		`[a-z][a-z0-9-]+:[a-zA-Z0-9][a-zA-Z0-9._-]+` +
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
