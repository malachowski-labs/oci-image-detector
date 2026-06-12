package generic_test

import (
	"strings"
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/generic"
)

func TestDetector_Match(t *testing.T) {
	det := generic.New()
	cases := []struct {
		path string
		want bool
	}{
		// Files generic should handle.
		{"script.sh", true},
		{"config.json", true},
		{"deploy.yaml", true},
		{"README.md", true},
		{"some/nested/file.txt", true},
		// Specialist files generic must NOT handle.
		{"Dockerfile", false},
		{"Dockerfile.dev", false},
		{"app.dockerfile", false},
		{"main.tf", false},
		{"terraform.tfvars", false},
		{"values.yaml", false},
		{"values.yml", false},
		{"charts/app/values.yaml", false},
	}
	for _, tc := range cases {
		if got := det.Match(tc.path); got != tc.want {
			t.Errorf("Match(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestDetector_Detect(t *testing.T) {
	det := generic.New()

	tests := []struct {
		name     string
		content  string
		wantRaws []string
	}{
		{
			name:     "registry/repo:tag in shell script",
			content:  `docker pull ghcr.io/org/app:v1.2.3`,
			wantRaws: []string{"ghcr.io/org/app:v1.2.3"},
		},
		{
			name:     "multiple refs on same line",
			content:  `images: ghcr.io/org/a:v1 ghcr.io/org/b:v2`,
			wantRaws: []string{"ghcr.io/org/a:v1", "ghcr.io/org/b:v2"},
		},
		{
			name:     "ref in JSON",
			content:  `{"image": "123456789.dkr.ecr.us-east-1.amazonaws.com/app:latest"}`,
			wantRaws: []string{"123456789.dkr.ecr.us-east-1.amazonaws.com/app:latest"},
		},
		{
			name:     "host:port registry with digest",
			content:  `localhost:5000/team/app@sha256:` + strings.Repeat("a", 64),
			wantRaws: []string{"localhost:5000/team/app@sha256:" + strings.Repeat("a", 64)},
		},
		{
			name:     "no refs in plain text",
			content:  "Hello world\nThis is just text\n",
			wantRaws: nil,
		},
		{
			// The generic detector is strict: it only matches host-qualified
			// references with a tag or digest. Bare Docker Hub library images
			// have no registry host and are left to the specialist detectors,
			// which have file-format context to recognise them safely.
			name: "bare library images are not matched",
			content: "docker pull nginx:1.25\n" +
				"image: redis:alpine\n" +
				"FROM_IMAGE=postgres:14\n",
			wantRaws: nil,
		},
		{
			// Regression for #24: trailing sentence punctuation must not be
			// captured. The tag-less "gcr.io/project/image." has no version
			// qualifier and is ignored entirely under the strict rule.
			name: "trailing punctuation is not captured",
			content: "Pull gcr.io/project/image.\n" +
				"Use ghcr.io/org/app:v1, then deploy.\n",
			wantRaws: []string{"ghcr.io/org/app:v1"},
		},
		{
			// Regression for #24: bare "org/repo" tokens are fractions or file
			// paths, not images — no registry host, so never matched.
			name: "bare org/repo is not an image",
			content: "ratio is 0/2 and 1/4\n" +
				"see 01-10-installation/overview.md\n" +
				"docs/setup.md mentions myorg/myimage\n",
			wantRaws: nil,
		},
		{
			// Regression for #24: bare "name:tag" config pairs have no registry
			// host and must not be reported.
			name: "bare name:tag config pairs are not images",
			content: "category: /language:go\n" +
				`"extends": ["config:recommended"]` + "\n" +
				"permissions need packages:write\n",
			wantRaws: nil,
		},
		{
			// Regression for #24: a dotted URL path without a tag (e.g. a JSON
			// schema link) is not an image reference.
			name:     "dotted url path without tag is not an image",
			content:  `"$schema": "https://docs.renovatebot.com/renovate-schema.json"`,
			wantRaws: nil,
		},
		{
			// Regression for #30: Go file:line references in stack traces match
			// the image shape but end in a source-file extension, so they are
			// not reported.
			name: "go source-file references are not images",
			content: `"github.com/kyma-project/test-infra/cmd/logging/main.go:47"` + "\n" +
				`"v0.0.1-go1.25.2.darwin-arm64/src/runtime/proc.go:285"` + "\n",
			wantRaws: nil,
		},
		{
			// Regression for #30: a host-qualified image with an integer tag is
			// a real reference and must be kept (a blanket numeric-tag filter
			// would wrongly drop it).
			name:     "host-qualified image with integer tag is kept",
			content:  `image: gcr.io/myproj/redis:7`,
			wantRaws: []string{"gcr.io/myproj/redis:7"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := det.Detect("file.sh", []byte(tc.content))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(findings) != len(tc.wantRaws) {
				t.Fatalf("got %d findings, want %d: %v", len(findings), len(tc.wantRaws), findings)
			}
			for i, f := range findings {
				if f.Ref.Raw != tc.wantRaws[i] {
					t.Errorf("finding[%d].Raw = %q, want %q", i, f.Ref.Raw, tc.wantRaws[i])
				}
				if f.Strategy != generic.Strategy {
					t.Errorf("finding[%d].Strategy = %q, want %q", i, f.Strategy, generic.Strategy)
				}
			}
		})
	}
}
