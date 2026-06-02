package reporter_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/reporter"
	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

func TestJSONFile_emptyFindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.json")
	r := reporter.NewJSONFile(path)
	if err := r.Report(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read output file: %v", err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, data)
	}

	// findings must be a JSON array, not null.
	var findings []json.RawMessage
	if err := json.Unmarshal(doc["findings"], &findings); err != nil {
		t.Fatalf("findings is not an array: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected empty findings array, got %d items", len(findings))
	}
}

func TestJSONFile_parsedFinding(t *testing.T) {
	ref, err := domain.NewParsedImageRef(
		"ghcr.io/org/app:v1.0.0", "ghcr.io", "org/app", "v1.0.0", "",
	)
	if err != nil {
		t.Fatalf("NewParsedImageRef: %v", err)
	}
	findings := []domain.Finding{
		{
			Ref:      ref,
			FilePath: "charts/values.yaml",
			Line:     5,
			Strategy: "helm",
		},
	}

	path := filepath.Join(t.TempDir(), "out.json")
	if err := reporter.NewJSONFile(path).Report(findings); err != nil {
		t.Fatalf("Report: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	type refJSON struct {
		Raw        string `json:"raw"`
		Canonical  string `json:"canonical"`
		Registry   string `json:"registry"`
		Repository string `json:"repository"`
		Tag        string `json:"tag"`
		Parsed     bool   `json:"parsed"`
	}
	type findingJSON struct {
		FilePath string  `json:"file_path"`
		Line     uint    `json:"line"`
		Strategy string  `json:"strategy"`
		Ref      refJSON `json:"ref"`
	}
	type doc struct {
		Findings []findingJSON `json:"findings"`
	}
	var out doc
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v\noutput: %s", err, data)
	}

	if len(out.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(out.Findings))
	}
	f := out.Findings[0]
	if f.FilePath != "charts/values.yaml" {
		t.Errorf("file_path = %q, want %q", f.FilePath, "charts/values.yaml")
	}
	if f.Line != 5 {
		t.Errorf("line = %d, want 5", f.Line)
	}
	if f.Strategy != "helm" {
		t.Errorf("strategy = %q, want %q", f.Strategy, "helm")
	}
	if f.Ref.Raw != "ghcr.io/org/app:v1.0.0" {
		t.Errorf("ref.raw = %q", f.Ref.Raw)
	}
	if f.Ref.Canonical != "ghcr.io/org/app:v1.0.0" {
		t.Errorf("ref.canonical = %q", f.Ref.Canonical)
	}
	if !f.Ref.Parsed {
		t.Errorf("ref.parsed should be true")
	}
}

func TestJSONFile_unresolvedFinding(t *testing.T) {
	findings := []domain.Finding{
		{
			Ref:      domain.NewImageRef("${var.image}:v1"),
			FilePath: "infra/main.tf",
			Line:     7,
			Strategy: "terraform",
		},
	}

	path := filepath.Join(t.TempDir(), "out.json")
	if err := reporter.NewJSONFile(path).Report(findings); err != nil {
		t.Fatalf("Report: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	type refJSON struct {
		Raw       string `json:"raw"`
		Canonical string `json:"canonical"`
		Parsed    bool   `json:"parsed"`
	}
	type findingJSON struct {
		Ref refJSON `json:"ref"`
	}
	type doc struct {
		Findings []findingJSON `json:"findings"`
	}
	var out doc
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(out.Findings))
	}
	f := out.Findings[0].Ref
	if f.Parsed {
		t.Errorf("unresolved ref should have parsed=false")
	}
	if f.Canonical != "" {
		t.Errorf("unresolved ref should have no canonical, got %q", f.Canonical)
	}
	if f.Raw != "${var.image}:v1" {
		t.Errorf("raw = %q", f.Raw)
	}
}

func TestJSONFile_endsWithNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.json")
	if err := reporter.NewJSONFile(path).Report(nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Errorf("file should end with newline, got: %q", string(data[max(0, len(data)-5):]))
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
