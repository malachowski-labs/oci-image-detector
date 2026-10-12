package domain_test

import (
	"testing"

	"github.com/malachowski-labs/oci-image-detector/pkg/domain"
)

func TestNewImageRef(t *testing.T) {
	ref := domain.NewImageRef("${var.image}")
	if ref.Raw != "${var.image}" {
		t.Errorf("Raw = %q, want %q", ref.Raw, "${var.image}")
	}
	if ref.Parsed {
		t.Error("Parsed should be false for NewImageRef")
	}
}

func TestNewParsedImageRef_valid(t *testing.T) {
	ref := domain.NewParsedImageRef("nginx:1.25", "index.docker.io", "library/nginx", "1.25", "")
	if !ref.Parsed {
		t.Error("Parsed should be true")
	}
	if ref.Registry != "index.docker.io" {
		t.Errorf("Registry = %q, want %q", ref.Registry, "index.docker.io")
	}
}

func TestImageRef_String(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"non-empty raw", "ghcr.io/org/app:v1.2.3"},
		{"empty raw", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ref := domain.NewImageRef(tc.raw)
			if got := ref.Raw; got != tc.raw {
				t.Errorf("String() = %q, want %q", got, tc.raw)
			}
		})
	}
}

func TestImageRef_Canonical(t *testing.T) {
	tests := []struct {
		name string
		ref  domain.ImageRef
		want string
	}{
		{
			name: "unparsed returns raw",
			ref:  domain.NewImageRef("${var.image}"),
			want: "${var.image}",
		},
		{
			name: "tag reference",
			ref:  domain.NewParsedImageRef("nginx:1.25", "index.docker.io", "library/nginx", "1.25", ""),
			want: "index.docker.io/library/nginx:1.25",
		},
		{
			name: "implicit latest tag",
			ref:  domain.NewParsedImageRef("nginx", "index.docker.io", "library/nginx", "", ""),
			want: "index.docker.io/library/nginx:latest",
		},
		{
			name: "digest reference",
			ref:  domain.NewParsedImageRef("ghcr.io/org/app@sha256:abc123", "ghcr.io", "org/app", "", "sha256:abc123"),
			want: "ghcr.io/org/app@sha256:abc123",
		},
		{
			name: "digest takes precedence over tag",
			ref:  domain.NewParsedImageRef("ghcr.io/org/app:v1@sha256:abc123", "ghcr.io", "org/app", "v1", "sha256:abc123"),
			want: "ghcr.io/org/app@sha256:abc123",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ref.Canonical(); got != tc.want {
				t.Errorf("Canonical() = %q, want %q", got, tc.want)
			}
		})
	}
}
