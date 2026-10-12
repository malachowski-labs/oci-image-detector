package domain

// Strategy is the short, stable identifier of the detector that produced a Finding.
// Each adapter package declares its own constant of this type; the domain
// package defines only the type itself and imposes no constraints on the value.
type Strategy string

// Finding pairs an ImageRef with its precise location in the scanned file tree.
// It is the primary output of any Detector and the primary input to any Reporter.
type Finding struct {
	// Ref is the detected OCI image reference.
	Ref ImageRef

	// FilePath is the slash-separated path to the source file,
	// relative to the scan root directory.
	FilePath string

	// Line is the 1-based line number where the image reference was found.
	// 0 means line information is not available for this detection strategy.
	Line uint

	// Strategy is the identifier of the detector that produced this finding.
	Strategy Strategy
}
