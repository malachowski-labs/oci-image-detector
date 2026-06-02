package reporter

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// jsonFinding is the JSON wire representation of a single finding.
type jsonFinding struct {
	FilePath string  `json:"file_path"`
	Line     uint    `json:"line"`
	Strategy string  `json:"strategy"`
	Ref      jsonRef `json:"ref"`
}

// jsonRef is the JSON wire representation of an image reference.
type jsonRef struct {
	Raw        string `json:"raw"`
	Canonical  string `json:"canonical,omitempty"`
	Registry   string `json:"registry,omitempty"`
	Repository string `json:"repository,omitempty"`
	Tag        string `json:"tag,omitempty"`
	Digest     string `json:"digest,omitempty"`
	Parsed     bool   `json:"parsed"`
}

// jsonReport is the top-level JSON document written by JSONFile.
type jsonReport struct {
	// Findings is always a JSON array, never null, even when empty.
	Findings []jsonFinding `json:"findings"`
}

// JSONFile writes a machine-readable JSON report to a file path.
type JSONFile struct {
	path string
}

// NewJSONFile returns a JSONFile reporter that writes to the given path.
// The file is created or truncated on each Report call.
func NewJSONFile(path string) *JSONFile {
	return &JSONFile{path: path}
}

// Report implements port.Reporter.
func (r *JSONFile) Report(findings []domain.Finding) error {
	report := jsonReport{
		// Initialise to an empty slice so the JSON encodes as [] not null.
		Findings: make([]jsonFinding, 0, len(findings)),
	}

	for _, f := range findings {
		jf := jsonFinding{
			FilePath: f.FilePath,
			Line:     f.Line,
			Strategy: string(f.Strategy),
			Ref: jsonRef{
				Raw:        f.Ref.Raw,
				Registry:   f.Ref.Registry,
				Repository: f.Ref.Repository,
				Tag:        f.Ref.Tag,
				Digest:     f.Ref.Digest,
				Parsed:     f.Ref.Parsed,
			},
		}
		if f.Ref.Parsed {
			jf.Ref.Canonical = f.Ref.Canonical()
		}
		report.Findings = append(report.Findings, jf)
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("json: marshal: %w", err)
	}

	// Ensure the file ends with a newline — friendly for POSIX tooling.
	data = append(data, '\n')

	if err := os.WriteFile(r.path, data, 0o644); err != nil {
		return fmt.Errorf("json: write %q: %w", r.path, err)
	}

	return nil
}
