package generic_test

import (
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
			// Bare Docker Hub official image: no registry or namespace prefix.
			name:     "bare official image name:tag",
			content:  `docker pull nginx:1.25`,
			wantRaws: []string{"nginx:1.25"},
		},
		{
			name:     "bare official image with alpine tag",
			content:  `image: redis:alpine`,
			wantRaws: []string{"redis:alpine"},
		},
		{
			name:     "bare official image with numeric tag",
			content:  `FROM_IMAGE=postgres:14`,
			wantRaws: []string{"postgres:14"},
		},
		{
			name:     "no refs in plain text",
			content:  "Hello world\nThis is just text\n",
			wantRaws: nil,
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
