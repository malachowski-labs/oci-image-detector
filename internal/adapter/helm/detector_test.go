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
			name: "repository and tag",
			content: `
image:
  repository: nginx
  tag: "1.25"
`,
			wantRaws: []string{"nginx:1.25"},
		},
		{
			name: "repository without tag",
			content: `
image:
  repository: ghcr.io/org/app
`,
			wantRaws: []string{"ghcr.io/org/app"},
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
			name: "registry without tag",
			content: `
image:
  registry: myregistry.example.com
  repository: myapp
`,
			wantRaws: []string{"myregistry.example.com/myapp"},
		},
		{
			name: "multiple images",
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
