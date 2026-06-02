package reporter_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/reporter"
	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

func mustParsedRef(raw, registry, repo, tag string) domain.ImageRef {
	ref, err := domain.NewParsedImageRef(raw, registry, repo, tag, "")
	if err != nil {
		panic(err)
	}
	return ref
}

func TestStdout_noFindings(t *testing.T) {
	var buf bytes.Buffer
	r := reporter.NewStdout(&buf)
	if err := r.Report(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No image references found") {
		t.Errorf("expected empty-findings message, got: %q", buf.String())
	}
}

func TestStdout_parsedRefWithDifferentCanonical(t *testing.T) {
	findings := []domain.Finding{
		{
			Ref:      mustParsedRef("nginx:1.25", "index.docker.io", "library/nginx", "1.25"),
			FilePath: "Dockerfile",
			Line:     1,
			Strategy: "dockerfile",
		},
	}
	var buf bytes.Buffer
	if err := reporter.NewStdout(&buf).Report(findings); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "nginx:1.25") {
		t.Errorf("expected raw ref in output, got: %q", out)
	}
	if !strings.Contains(out, "index.docker.io/library/nginx:1.25") {
		t.Errorf("expected canonical in output, got: %q", out)
	}
	if !strings.Contains(out, "[dockerfile]") {
		t.Errorf("expected strategy in output, got: %q", out)
	}
}

func TestStdout_unresolvedRef(t *testing.T) {
	findings := []domain.Finding{
		{
			Ref:      domain.NewImageRef("${var.image}:v1"),
			FilePath: "infra/main.tf",
			Line:     10,
			Strategy: "terraform",
		},
	}
	var buf bytes.Buffer
	if err := reporter.NewStdout(&buf).Report(findings); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "(unresolved)") {
		t.Errorf("expected (unresolved) marker, got: %q", out)
	}
	if !strings.Contains(out, "${var.image}:v1") {
		t.Errorf("expected raw ref, got: %q", out)
	}
}

func TestStdout_summaryLine(t *testing.T) {
	findings := []domain.Finding{
		{
			Ref:      mustParsedRef("ghcr.io/org/app:v1", "ghcr.io", "org/app", "v1"),
			FilePath: "values.yaml",
			Line:     5,
			Strategy: "helm",
		},
		{
			Ref:      mustParsedRef("ghcr.io/org/svc:v2", "ghcr.io", "org/svc", "v2"),
			FilePath: "values.yaml",
			Line:     12,
			Strategy: "helm",
		},
	}
	var buf bytes.Buffer
	if err := reporter.NewStdout(&buf).Report(findings); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Found 2 image reference(s)") {
		t.Errorf("expected summary line, got: %q", buf.String())
	}
}
