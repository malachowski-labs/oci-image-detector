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
			if imageref.IsSourceFile(ref) {
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
