// Package generic provides a regex-based fallback Detector for files not
// handled by any specialist detector.
package generic

import (
	"bytes"
	"strings"

	"github.com/malachowski-labs/oci-image-detector/pkg/domain"
	"github.com/malachowski-labs/oci-image-detector/pkg/imageref"
	"github.com/malachowski-labs/oci-image-detector/pkg/port"
	"github.com/malachowski-labs/oci-image-detector/pkg/registry"
	"github.com/malachowski-labs/oci-image-detector/pkg/suppress"
)

// Strategy is the stable identifier for this detection strategy.
const Strategy domain.Strategy = "generic"

// Detector implements port.Detector as a regex-based fallback.
type Detector struct{}

// init makes the detector available to every binary that links this package.
func init() { registry.MustRegister(&Detector{}) }

// Name implements port.Detector.
func (d *Detector) Name() string { return string(Strategy) }

// Tier implements port.Detector.
func (d *Detector) Tier() port.Tier { return port.TierFallback }

// Match implements port.Detector.
// Returns true for every file: as a fallback this detector is only consulted
// after every specialist has declined the file, and the scan service holds
// back the files a directory-aware detector owns.
func (d *Detector) Match(string) bool { return true }

// Detect implements port.Detector.
// Scans each line for candidate image reference patterns and validates them
// with go-containerregistry.
func (d *Detector) Detect(filePath string, content []byte) ([]domain.Finding, error) {
	var findings []domain.Finding
	var lineNum uint

	for raw := range bytes.Lines(content) {
		lineNum++
		line := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")

		if suppress.IsIgnoredLine(line) {
			continue
		}

		for _, match := range imageref.Candidates(line) {
			ref := imageref.Parse(match)
			if isSourceFileReference(ref) {
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

	return findings, nil
}

// sourceFileExtensions are file extensions that identify a source-code or
// markup file. An OCI repository path never ends in one of these, so a
// candidate whose final path segment carries such an extension is a file
// reference (e.g. a Go stack trace "github.com/org/repo/main.go:47", where the
// ":47" is a line number, not a tag) rather than an image reference.
//
// A numeric-tag filter would also reject those stack traces, but it would
// wrongly drop legitimate host-qualified images with integer tags such as
// "gcr.io/proj/redis:7" — so we key off the source-file extension instead.
var sourceFileExtensions = map[string]bool{
	"go": true, "py": true, "js": true, "ts": true, "jsx": true, "tsx": true,
	"rb": true, "java": true, "rs": true, "c": true, "cc": true, "cpp": true,
	"cxx": true, "h": true, "hpp": true, "cs": true, "php": true, "kt": true,
	"kts": true, "swift": true, "scala": true, "sh": true, "bash": true,
	"pl": true, "pm": true, "lua": true, "ex": true, "exs": true, "erl": true,
	"clj": true, "dart": true, "groovy": true, "m": true, "mm": true,
}

// isSourceFileReference reports whether ref's repository path points at a
// source-code file (its final path segment ends in a known source-file
// extension) rather than at an image. Used to drop file:line references that
// happen to match the image-reference shape.
func isSourceFileReference(ref domain.ImageRef) bool {
	repo := ref.Repository
	if repo == "" {
		repo = ref.Raw
	}
	last := repo
	if i := strings.LastIndexByte(repo, '/'); i >= 0 {
		last = repo[i+1:]
	}
	dot := strings.LastIndexByte(last, '.')
	if dot < 0 {
		return false
	}
	return sourceFileExtensions[last[dot+1:]]
}
