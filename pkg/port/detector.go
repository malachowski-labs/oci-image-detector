// Package port defines the interfaces that form the boundaries of the
// application's hexagonal architecture.
//
// Driving ports (input) are called by the CLI adapter to trigger use-cases.
// Driven ports (output) are implemented by adapters and injected into services.
package port

import (
	"fmt"
	"io/fs"

	"github.com/malachowski-labs/oci-image-detector/pkg/domain"
)

// Tier is the priority class a Detector declares for itself. It replaces
// positional ordering: every specialist is consulted before any fallback, so a
// detector's precedence is a property of the detector rather than of where it
// happens to be registered.
type Tier int

const (
	// TierSpecialist marks a detector that understands one file format and
	// claims only the files of that format.
	TierSpecialist Tier = iota

	// TierFallback marks a detector that accepts files no specialist claimed.
	TierFallback
)

// String returns the lower-case name of the tier, for logs and error messages.
func (t Tier) String() string {
	switch t {
	case TierSpecialist:
		return "specialist"
	case TierFallback:
		return "fallback"
	default:
		return fmt.Sprintf("Tier(%d)", int(t))
	}
}

// Detector is a driven port implemented by each file-type-specific detection
// strategy. The ScanService iterates registered detectors, calls Match to find
// the right one for each file, then calls Detect to extract image references.
type Detector interface {
	// Name returns the short, stable identifier for this detection strategy.
	// Must be a non-empty string that is stable across versions; it is used
	// as Finding.Strategy for every finding this detector produces.
	Name() string

	// Tier returns the priority class of this detector. The registry orders
	// detectors by tier, so Match never has to know which other detectors
	// exist in order to yield to them.
	Tier() Tier

	// Match reports whether this detector should handle the given file path.
	// Path is slash-separated and relative to the scan root.
	Match(path string) bool

	// Detect scans the content of a single file and returns all findings.
	// path is slash-separated and relative to the scan root; it must be
	// used as Finding.FilePath in every returned finding.
	// Returning an error does not stop the overall scan; the service logs it
	// and moves on to the next file.
	Detect(path string, content []byte) ([]domain.Finding, error)
}

// DirectoryAwareDetector is a driven port for strategies that require access
// to sibling files for resolution (e.g. Terraform resolving variable values
// across .tf and .tfvars files in the same directory).
//
// It is intentionally separate from Detector — a dir-aware strategy does not
// have a meaningful Match(file) method, and embedding Detector would require
// implementing a method that the ScanService must never call.
//
// DirectoryAwareDetectors declare no Tier: they are always consulted before
// file-level Detectors, and there is no fallback notion for directories.
//
// The ScanService accepts a separate slice of DirectoryAwareDetectors. For
// each directory in the scan tree it calls MatchDir; if true it calls
// DetectDir. File-level Detectors are not consulted for files inside a
// directory claimed by a DirectoryAwareDetector.
//
// The ScanService is responsible for prepending dirPath to all returned
// Finding.FilePath values — adapters return paths relative to dir only.
type DirectoryAwareDetector interface {
	// Name returns the short, stable identifier for this detection strategy.
	// Same contract as Detector.Name.
	Name() string

	// MatchDir reports whether this detector should process the given directory.
	// dirPath is slash-separated and relative to the scan root.
	// entries are the direct children of the directory — implementations use
	// them to inspect filenames (e.g. "does this dir contain .tf files?")
	// without requiring an additional FS read.
	MatchDir(dirPath string, entries []fs.DirEntry) bool

	// DetectDir scans an entire directory in one pass.
	// dir is an fs.FS rooted at the directory being scanned.
	// Returned Finding.FilePath values must be relative to dir (filename only
	// or sub-path within dir); the ScanService prepends dirPath.
	DetectDir(dir fs.FS) ([]domain.Finding, error)
}
