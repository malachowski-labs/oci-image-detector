package service

import (
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

func TestSortFindings(t *testing.T) {
	findings := []domain.Finding{
		{FilePath: "b/file.tf", Line: 1, Ref: domain.ImageRef{Raw: "nginx:latest"}},
		{FilePath: "a/file.tf", Line: 2, Ref: domain.ImageRef{Raw: "redis:7"}},
		{FilePath: "a/file.tf", Line: 2, Ref: domain.ImageRef{Raw: "alpine:3"}},
		{FilePath: "a/file.tf", Line: 1, Ref: domain.ImageRef{Raw: "ubuntu:22.04"}},
	}

	sortFindings(findings)

	want := []struct {
		path string
		line uint
		raw  string
	}{
		{"a/file.tf", 1, "ubuntu:22.04"},
		{"a/file.tf", 2, "alpine:3"},
		{"a/file.tf", 2, "redis:7"},
		{"b/file.tf", 1, "nginx:latest"},
	}

	for i, w := range want {
		f := findings[i]
		if f.FilePath != w.path || f.Line != w.line || f.Ref.Raw != w.raw {
			t.Errorf("findings[%d] = {%q %d %q}, want {%q %d %q}",
				i, f.FilePath, f.Line, f.Ref.Raw,
				w.path, w.line, w.raw)
		}
	}
}
