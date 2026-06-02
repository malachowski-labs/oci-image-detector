// Package dockerfile provides a Detector for Dockerfile files.
package dockerfile

import (
	"bufio"
	"bytes"
	"path"
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

// Detect implements port.Detector.
// Parses FROM instructions and returns one Finding per non-scratch base image.
func (d *Detector) Detect(filePath string, content []byte) ([]domain.Finding, error) {
	var findings []domain.Finding
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, maxScanTokenSize), maxScanTokenSize)
	var lineNum uint

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		upper := strings.ToUpper(line)
		if !strings.HasPrefix(upper, "FROM ") {
			continue
		}

		raw, ok := parseFrom(line)
		if !ok || strings.EqualFold(raw, "scratch") {
			continue
		}

		ref := imageref.Parse(raw)
		findings = append(findings, domain.Finding{
			Ref:      ref,
			FilePath: filePath,
			Line:     lineNum,
			Strategy: Strategy,
		})
	}

	return findings, scanner.Err()
}

// parseFrom extracts the image reference from a FROM instruction.
// Handles: FROM [--platform=<p>] <image> [AS <name>]
func parseFrom(line string) (string, bool) {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return "", false
	}
	// parts[0] is "FROM"; skip --flag arguments.
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, "--") {
			continue
		}
		return p, true
	}
	return "", false
}
