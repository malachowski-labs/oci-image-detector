package helm_test

import (
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/helm"
)

func TestDetector_Match(t *testing.T) {
	det := helm.New()
	cases := []struct {
		path string
		want bool
	}{
		{"values.yaml", true},
		{"values.yml", true},
		{"charts/myapp/values.yaml", true},
		{"Values.yaml", true},
		{"values.json", false},
		{"other.yaml", false},
		{"Dockerfile", false},
	}
	for _, tc := range cases {
		if got := det.Match(tc.path); got != tc.want {
			t.Errorf("Match(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestDetector_Detect(t *testing.T) {
	det := helm.New()

	tests := []struct {
		name     string
		content  string
		wantRaws []string
	}{
		{
			name: "repository and tag under image parent",
			content: `
image:
  repository: nginx
  tag: "1.25"
`,
			wantRaws: []string{"nginx:1.25"},
		},
		{
			name: "registry repository and tag (three-key Bitnami convention)",
			content: `
image:
  registry: myregistry.example.com
  repository: myapp
  tag: "1.0"
`,
			wantRaws: []string{"myregistry.example.com/myapp:1.0"},
		},
		{
			name: "repository and digest under image parent",
			content: `
image:
  repository: ghcr.io/org/app
  digest: sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1
`,
			wantRaws: []string{"ghcr.io/org/app@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1"},
		},
		{
			name: "tag and digest both present (canonical form keeps both)",
			content: `
image:
  repository: ghcr.io/org/app
  tag: v1
  digest: sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1
`,
			wantRaws: []string{"ghcr.io/org/app:v1@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1"},
		},
		{
			name: "multiple images under image-shaped parents",
			content: `
frontend:
  image:
    repository: nginx
    tag: "1.25"
backend:
  image:
    repository: ghcr.io/org/api
    tag: v2.0.0
`,
			wantRaws: []string{"nginx:1.25", "ghcr.io/org/api:v2.0.0"},
		},
		{
			name: "image-shaped sibling key (mainImage)",
			content: `
mainImage:
  repository: nginx
  tag: "1.25"
`,
			wantRaws: []string{"nginx:1.25"},
		},
		{
			name: "image-shaped sibling key with hyphen (init-image)",
			content: `
init-image:
  repository: ghcr.io/org/init
  tag: v1
`,
			wantRaws: []string{"ghcr.io/org/init:v1"},
		},
		{
			name: "image-shaped sibling key with underscore (metrics_image)",
			content: `
metrics_image:
  repository: ghcr.io/org/metrics
  tag: v1
`,
			wantRaws: []string{"ghcr.io/org/metrics:v1"},
		},
		{
			name: "image sequence inherits parent key",
			content: `
images:
  - repository: nginx
    tag: "1.25"
  - repository: ghcr.io/org/api
    tag: v2.0.0
`,
			wantRaws: []string{"nginx:1.25", "ghcr.io/org/api:v2.0.0"},
		},
		{
			name: "template tag kept as raw",
			content: `
image:
  repository: nginx
  tag: "{{ .Values.imageTag }}"
`,
			wantRaws: []string{"nginx:{{ .Values.imageTag }}"},
		},
		{
			name:     "no image blocks",
			content:  "replicaCount: 3\n",
			wantRaws: nil,
		},
		{
			name:     "empty file",
			content:  "",
			wantRaws: nil,
		},

		// --- Regression guards (issue #36) ---------------------------------

		{
			name: "ocm_repo_mappings repository key is not an image block",
			content: `
ocm_repo_mappings:
  - prefix: "kyma-project.io/module"
    repository: europe-docker.pkg.dev/kyma-project/kyma-modules
`,
			wantRaws: nil,
		},
		{
			name: "rescoring_ruleset.ref.repository pointing at a Git URL is not an image block",
			content: `
rescoring_ruleset:
  cfg_name: example
  ref:
    repository: https://example.com/org/repo
    path: rescorings.yaml
`,
			wantRaws: nil,
		},
		{
			name: "tag-less repository under image parent is rejected",
			content: `
image:
  repository: ghcr.io/org/app
`,
			wantRaws: nil,
		},
		{
			name: "registry+repository without tag is rejected",
			content: `
image:
  registry: myregistry.example.com
  repository: myapp
`,
			wantRaws: nil,
		},
		{
			name: "url literal under image parent is dropped by the candidate gate",
			content: `
image:
  repository: https://example.com/org/app
  tag: v1
`,
			wantRaws: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := det.Detect("values.yaml", []byte(tc.content))
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
				if f.Strategy != helm.Strategy {
					t.Errorf("finding[%d].Strategy = %q, want %q", i, f.Strategy, helm.Strategy)
				}
			}
		})
	}
}
