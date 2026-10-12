package helm_test

import (
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/helm"
)

func TestDetector_Match(t *testing.T) {
	det := &helm.Detector{}
	cases := []struct {
		path string
		want bool
	}{
		{"values.yaml", true},
		{"values.yml", true},
		{"charts/myapp/values.yaml", true},
		{"Values.yaml", true},
		{"values-prod.yaml", true},
		{"values.prod.yml", true},
		{"charts/myapp/prod-values.yaml", true},
		{"env/prod.values.yaml", true},
		{"values.json", false},
		{"other.yaml", false},
		{"myvalues.yaml", false},
		{"valuesprod.yaml", false},
		{"values/deploy.yaml", false},
		{"Dockerfile", false},
		{".github/workflows/values.yaml", false},
		{".github/actions/build/values.yml", false},
		{"sub/.GitHub/Workflows/Values.yaml", false},
	}
	for _, tc := range cases {
		if got := det.Match(tc.path); got != tc.want {
			t.Errorf("Match(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestDetector_Detect(t *testing.T) {
	det := &helm.Detector{}

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
		{
			name: "ignore annotation on repository suppresses the image",
			content: `
image:
  repository: nginx  # oci-image-detector:ignore
  tag: "1.25"
sidecarImage:
  repository: envoyproxy/envoy
  tag: v1.30
`,
			wantRaws: []string{"envoyproxy/envoy:v1.30"},
		},
		{
			name: "ignore annotation on any line of the block suppresses the image",
			content: `
image:
  repository: nginx
  tag: "1.25"  # oci-image-detector:ignore
initImage:
  registry: ghcr.io  # oci-image-detector:ignore
  repository: org/init
  tag: v1
metricsImage:
  repository: org/metrics
  pullPolicy: Always  # oci-image-detector:ignore
  tag: v2
`,
			wantRaws: []string{"org/metrics:v2"},
		},
		{
			name: "pinned string under an image-shaped key",
			content: `
image: nginx:1.25
sidecar:
  image: ghcr.io/org/sidecar:v1
initImage: busybox@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1
extraImages:
  - redis:7
  - "registry.example.com:5000/org/tool:v2"
`,
			wantRaws: []string{
				"nginx:1.25",
				"ghcr.io/org/sidecar:v1",
				"busybox@sha256:abc123def456abc123def456abc123def456abc123def456abc123def456abc1",
				"redis:7",
				"registry.example.com:5000/org/tool:v2",
			},
		},
		{
			name: "string under an image-shaped key that is not a pinned reference",
			content: `
image: nginx
backgroundImage: assets/logo.png
heroImage: https://example.com/img/hero:v1
pullImage: "true"
image_note: "see docs: pick an image"
`,
			wantRaws: nil,
		},
		{
			name: "templated string under an image-shaped key is kept raw",
			content: `
image: "{{ .Values.global.registry }}/app:{{ .Values.tag }}"
`,
			wantRaws: []string{"{{ .Values.global.registry }}/app:{{ .Values.tag }}"},
		},
		{
			name: "fully-qualified references outside image-shaped keys",
			content: `
extraEnv:
  - name: RELATED_IMAGE
    value: quay.io/org/related:v2
  - name: SHORT_FORM
    value: nginx:1.25
args: ["--sidecar=ghcr.io/org/sidecar:v1"]
digests:
  ghcr.io/org/keyed:v3: sha256:abc
script: |
  docker pull ghcr.io/org/tool:v4
`,
			wantRaws: []string{
				"quay.io/org/related:v2",
				"ghcr.io/org/sidecar:v1",
				"ghcr.io/org/keyed:v3",
				"ghcr.io/org/tool:v4",
			},
		},
		{
			name: "ignore annotation on a string value",
			content: `
image: nginx:1.25  # oci-image-detector:ignore
extraEnv:
  - value: quay.io/org/related:v2  # oci-image-detector:ignore
  - value: quay.io/org/kept:v2
`,
			wantRaws: []string{"quay.io/org/kept:v2"},
		},
		{
			name: "longer marker is not the ignore annotation",
			content: `
image:
  repository: nginx  # oci-image-detector:ignored
  tag: "1.25"
`,
			wantRaws: []string{"nginx:1.25"},
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
