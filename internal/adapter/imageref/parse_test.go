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
		wantTag    string
		wantDigest string
	}{
		{
			raw:        "nginx:1.25",
			wantParsed: true,
			wantCanon:  "index.docker.io/library/nginx:1.25",
			wantTag:    "1.25",
		},
		{
			raw:        "nginx",
			wantParsed: true,
			wantCanon:  "index.docker.io/library/nginx:latest",
			wantTag:    "latest",
		},
		{
			raw:        "ghcr.io/org/app:v1.2.3",
			wantParsed: true,
			wantCanon:  "ghcr.io/org/app:v1.2.3",
			wantTag:    "v1.2.3",
		},
		{
			raw:        "ghcr.io/org/app@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1",
			wantParsed: true,
			wantDigest: "sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1",
		},
		{
			// Tag + digest: both fields populated, but Canonical uses the digest
			// alone (https://github.com/malachowski-labs/oci-image-detector/issues/38).
			raw:        "golang:1.26-alpine@sha256:7a3e50096189ad57c9f9f865e7e4aa8585ed1585248513dc5cda498e2f41812c",
			wantParsed: true,
			wantCanon:  "index.docker.io/library/golang@sha256:7a3e50096189ad57c9f9f865e7e4aa8585ed1585248513dc5cda498e2f41812c",
			wantTag:    "1.26-alpine",
			wantDigest: "sha256:7a3e50096189ad57c9f9f865e7e4aa8585ed1585248513dc5cda498e2f41812c",
		},
		{
			// Tag + digest behind an explicit host:port registry — the host's
			// colon must not be mistaken for the tag separator.
			raw:        "localhost:5000/app:v1@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1",
			wantParsed: true,
			wantCanon:  "localhost:5000/app@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1",
			wantTag:    "v1",
			wantDigest: "sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1",
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
			if ref.Tag != tc.wantTag {
				t.Errorf("Tag = %q, want %q", ref.Tag, tc.wantTag)
			}
			if ref.Digest != tc.wantDigest {
				t.Errorf("Digest = %q, want %q", ref.Digest, tc.wantDigest)
			}
		})
	}
}

func TestCandidates(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "fully qualified ref",
			in:   `image = "ghcr.io/org/app:v1.2.3"`,
			want: []string{"ghcr.io/org/app:v1.2.3"},
		},
		{
			name: "two refs on one line",
			in:   `images: gcr.io/proj/a:v1 gcr.io/proj/b:v2`,
			want: []string{"gcr.io/proj/a:v1", "gcr.io/proj/b:v2"},
		},
		{
			name: "bare library image has no registry host",
			in:   `image = "nginx:1.25"`,
			want: nil,
		},
		{
			name: "untagged ref is ignored",
			in:   `"core.gardener.cloud/v1beta1"`,
			want: nil,
		},
		{
			name: "provider name has no host or tag",
			in:   `"hashicorp/google"`,
			want: nil,
		},
		{
			// The host+path+tag-shaped tail of a WIF principal must not be
			// reported: it is preceded by "/" so it is part of the URI.
			name: "wif principal tail is rejected",
			in:   `"principalSet://iam.googleapis.com/pool/attribute.x:value"`,
			want: nil,
		},
		{
			name: "tagged image inside a URL is rejected",
			in:   `oci://registry.example.com/charts/app:1.0`,
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := imageref.Candidates(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("Candidates(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("Candidates(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
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

func TestIsIgnoredLine(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{`// "localhost:5000/image:v1" -> "localhost:5000/image"  // oci-image-detector:ignore`, true},
		{`FROM nginx:latest  # oci-image-detector:ignore`, true},
		{`image = "gcr.io/proj/app:v1"  # oci-image-detector:ignore`, true},
		{`ghcr.io/org/app:v1`, false},
		{`# oci-image-detector:ignoreXYZ is not a match`, false},
		{`oci-image-detector:ignore`, true}, // annotation without leading comment marker
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			if got := imageref.IsIgnoredLine(tc.line); got != tc.want {
				t.Errorf("IsIgnoredLine(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}
