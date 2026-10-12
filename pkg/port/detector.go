// Package port defines the interfaces that form the boundaries of the
// application's hexagonal architecture.
//
// The ports are implemented by adapters and injected into the scan service.
package port

import (
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

// Detector is a driven port implemented by each file-type-specific detection
// strategy. The ScanService iterates registered detectors, calls Match to find
// the right one for each file, then calls Detect to extract image references.
type Detector interface {
	// Name returns the short, stable identifier for this detection strategy.
	// Must be a non-empty string that is stable across versions; it is used
	// as Finding.Strategy for every finding this detector produces.
	Name() string

	// Tier returns the priority class of this detector. The registry orders
	// detectors by tier, so a fallback is only consulted for files no
	// specialist claimed. Detectors that share a tier must claim disjoint
	// sets of files: order within a tier is deterministic, not a precedence.
	Tier() Tier

	// Match reports whether this detector should handle the given file path.
	// Path is slash-separated and relative to the scan root.
	// A detector that needs to look at the content as well also implements
	// ContentMatcher.
	Match(path string) bool

	// Detect scans the content of a single file and returns all findings.
	// path is slash-separated and relative to the scan root; it must be
	// used as Finding.FilePath in every returned finding.
	// Returning an error does not stop the overall scan; the service logs it
	// and moves on to the next file.
	Detect(path string, content []byte) ([]domain.Finding, error)
}

// ContentMatcher is an optional extension of Detector for formats that follow
// no naming convention, so that a path alone cannot tell whether a file
// belongs to them (a Kubernetes manifest, for instance).
//
// A Detector that implements it claims a file only when both Match and
// MatchContent accept it; Match stays the cheap filter that spares the
// content check for most files. Such a detector is consulted after every
// specialist that claims files by path alone and before any fallback, so a
// claim by path always takes precedence over a claim by content.
type ContentMatcher interface {
	// MatchContent reports whether this detector should handle the file,
	// judging by its content. It is only called for paths Match accepted.
	// path is slash-separated and relative to the scan root.
	MatchContent(path string, content []byte) bool
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
// For each directory in the scan tree, the scan root included, the ScanService
// calls MatchDir on every DirectoryAwareDetector and DetectDir on those that
// match. A match claims only the files the detector Owns: the other files of
// the directory go to the file-level Detectors, and its subdirectories are
// walked like any other.
//
// The ScanService is responsible for prepending dirPath to all returned
// Finding.FilePath values — adapters return paths relative to dir only.
type DirectoryAwareDetector interface {
	// Name returns the short, stable identifier for this detection strategy.
	// Same contract as Detector.Name.
	Name() string

	// Owns reports whether the file belongs to this detector's format.
	// filePath is slash-separated and relative to the scan root.
	// Owned files are never passed to a file-level Detector, whether or not
	// their directory matched, so the answer must depend on the path alone
	// and DetectDir must cover every owned file of a matching directory.
	Owns(filePath string) bool

	// MatchDir reports whether this detector should process the given directory.
	// dirPath is slash-separated and relative to the scan root.
	// entries are the direct children of the directory — implementations use
	// them to inspect filenames (e.g. "does this dir contain .tf files?")
	// without requiring an additional FS read.
	MatchDir(dirPath string, entries []fs.DirEntry) bool

	// DetectDir scans the owned files of a directory in one pass.
	// dir is an fs.FS rooted at the directory being scanned.
	// Returned Finding.FilePath values must be relative to dir (filename only
	// or sub-path within dir); the ScanService prepends dirPath.
	DetectDir(dir fs.FS) ([]domain.Finding, error)
}
