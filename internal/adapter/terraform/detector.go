// Package terraform provides a DirectoryAwareDetector for Terraform files.
// It processes entire directories at once so that variables and locals can be
// resolved across .tf and .tfvars files using the HashiCorp HCL parser and
// evaluator. Resolved strings are filtered with the same strict
// imageref.Candidates precision filter the generic detector uses.
//
// Static resolution is necessarily incomplete: Terraform's full function set,
// resource/data references, count/for_each and cross-module inputs live in
// Terraform core, not in the HCL library. What cannot be resolved is never
// guessed at; the literal strings around it are still scanned.
package terraform

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/malachowski-labs/oci-image-detector/internal/domain"
	"github.com/malachowski-labs/oci-image-detector/internal/registry"
)

// Strategy is the stable identifier for findings resolved from Terraform source.
const Strategy domain.Strategy = "terraform"

// Detector implements port.DirectoryAwareDetector for Terraform directories.
type Detector struct{}

// New returns a new Terraform Detector.
func New() *Detector { return &Detector{} }

// init makes the detector available to every binary that links this package.
func init() { registry.MustRegisterDir(New()) }

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
// It parses every .tf file in the directory once, builds a single scope
// (variable defaults overridden by .tfvars, plus locals), then scans every
// attribute against that scope and reports the image references found.
//
// Files that cannot be read or parsed, and expressions left unresolved because
// of the evaluation limits, are reported through the returned error; the
// findings gathered from everything else are returned alongside it.
func (d *Detector) DetectDir(dir fs.FS) ([]domain.Finding, error) {
	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		return nil, fmt.Errorf("terraform: read dir: %w", err)
	}

	files, errs := parseTFFiles(dir, entries)
	sc, scopeErrs := newScope(dir, entries, files)
	errs = append(errs, scopeErrs...)

	var findings []domain.Finding
	for _, f := range files {
		findings = append(findings, scanFile(f, sc)...)
	}
	if sc.limited {
		errs = append(errs, errLimited)
	}
	return findings, errors.Join(errs...)
}
