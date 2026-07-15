// Package terraform provides a DirectoryAwareDetector for Terraform files.
// It processes entire directories at once so that variables and locals can be
// resolved across .tf and .tfvars files using the HashiCorp HCL parser and
// evaluator. Because evaluated values are concrete cty values, image strings
// are filtered with the same strict imageref.Candidates precision filter the
// generic detector uses.
//
// Static resolution is necessarily incomplete: Terraform's full function set,
// resource/data references, count/for_each and cross-module inputs live in
// Terraform core, not in the HCL library. Expressions that cannot be fully
// resolved are skipped rather than reported, keeping precision high. The plan
// command (DetectPlan) covers those cases by consuming already-resolved
// `terraform show -json` output.
package terraform

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// Strategy is the stable identifier for findings resolved from Terraform source.
const Strategy domain.Strategy = "terraform"

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
//
// It parses every .tf file in the directory once, builds a single evaluation
// context (variable defaults overridden by .tfvars, plus locals evaluated to a
// fixpoint), then evaluates every attribute against that context and reports the
// image references found in any resolved string value.
func (d *Detector) DetectDir(dir fs.FS) ([]domain.Finding, error) {
	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		return nil, fmt.Errorf("terraform: read dir: %w", err)
	}

	files := parseTFFiles(dir, entries)
	if len(files) == 0 {
		return nil, nil
	}

	ctx := buildEvalContext(dir, entries, files)

	var findings []domain.Finding
	for _, f := range files {
		findings = append(findings, scanBody(f.name, f.body, ctx, f.lines)...)
	}
	return findings, nil
}
