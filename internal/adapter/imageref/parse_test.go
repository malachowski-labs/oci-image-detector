package imageref_test

import (
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/imageref"
	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

func TestParse(t *testing.T) {
	tests := []struct {
		raw        string
		wantParsed bool
		wantCanon  string
	}{
		{
			raw:        "nginx:1.25",
			wantParsed: true,
			wantCanon:  "index.docker.io/library/nginx:1.25",
		},
		{
			raw:        "nginx",
			wantParsed: true,
			wantCanon:  "index.docker.io/library/nginx:latest",
		},
		{
			raw:        "ghcr.io/org/app:v1.2.3",
			wantParsed: true,
			wantCanon:  "ghcr.io/org/app:v1.2.3",
		},
		{
			raw:        "ghcr.io/org/app@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1",
			wantParsed: true,
		},
		{
			// Terraform interpolation — must not be parsed.
			raw:        "${var.image}",
			wantParsed: false,
		},
		{
			// Shell variable — must not be parsed.
			raw:        "$IMAGE_TAG",
			wantParsed: false,
		},
		{
			// Go/Helm template — must not be parsed.
			raw:        "{{.Values.image}}",
			wantParsed: false,
		},
		{
			// Ruby interpolation — must not be parsed.
			raw:        "myregistry.io/app:#{env.tag}",
			wantParsed: false,
		},
		{
			// Python %-format — must not be parsed.
			raw:        "myregistry.io/app:%(tag)s",
			wantParsed: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			ref := imageref.Parse(tc.raw)
			if ref.Parsed != tc.wantParsed {
				t.Errorf("Parsed = %v, want %v (raw=%q)", ref.Parsed, tc.wantParsed, tc.raw)
			}
			if ref.Raw != tc.raw {
				t.Errorf("Raw = %q, want %q", ref.Raw, tc.raw)
			}
			if tc.wantCanon != "" {
				if got := ref.Canonical(); got != tc.wantCanon {
					t.Errorf("Canonical() = %q, want %q", got, tc.wantCanon)
				}
			}
		})
	}
}

func TestLooksLikeImage(t *testing.T) {
	parsedWithSlash := imageref.Parse("ghcr.io/org/app:v1")
	parsedWithColon := imageref.Parse("nginx:1.25")
	parsedBareWord := imageref.Parse("enabled") // parses as library/enabled:latest — must be rejected
	unparsedWithColon := domain.NewImageRef("${var.image}:v1")
	unparsedNoColon := domain.NewImageRef("${var.something}")

	cases := []struct {
		name string
		ref  domain.ImageRef
		want bool
	}{
		{"parsed with slash", parsedWithSlash, true},
		{"parsed with colon", parsedWithColon, true},
		{"parsed bare word", parsedBareWord, false},
		{"unparsed with colon", unparsedWithColon, true},
		{"unparsed no colon", unparsedNoColon, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := imageref.LooksLikeImage(tc.ref); got != tc.want {
				t.Errorf("LooksLikeImage(%q parsed=%v) = %v, want %v",
					tc.ref.Raw, tc.ref.Parsed, got, tc.want)
			}
		})
	}
}
