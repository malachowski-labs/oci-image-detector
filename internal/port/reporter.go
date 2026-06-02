package port

import (
	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// Reporter is a driven port implemented by output adapters (human-readable
// stdout, JSON file). The ScanService calls Report once with the complete,
// sorted slice of findings after the scan finishes.

// Reporter is a driven port implemented by output adapters (human-readable
// stdout, JSON file). The ScanService calls Report once with the complete,
// sorted slice of findings after the scan is finished.
type Reporter interface {
	// Report outputs all findings. The findings slice is always sorted by
	// FilePath → Line → Raw before this method is called.
	// Implementations must write to their configured destination and return
	// any I/O error to the caller.
	Report(findings []domain.Finding) error
}
