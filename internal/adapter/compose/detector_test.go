package compose_test

import (
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/compose"
)

func TestDetector_Match(t *testing.T) {
	det := compose.New()
	cases := []struct {
		path string
		want bool
	}{
		{"compose.yaml", true},
		{"compose.yml", true},
		{"docker-compose.yaml", true},
		{"deploy/docker-compose.yml", true},
		{"compose.prod.yaml", true},
		{"deploy/docker-compose.override.yml", true},
		{"Docker-Compose.YML", true},
		{"compose.json", false},            // not YAML
		{"compose", false},                 // no extension
		{"composer.yaml", false},           // different stem
		{"my-compose.yaml", false},         // different stem
		{"docker-compose/app.yaml", false}, // directory name, not file name
		{".github/workflows/compose.yaml", false},
		{".github/actions/build/docker-compose.yml", false},
		{"Dockerfile", false},
		{"values.yaml", false},
		{"main.tf", false},
	}
	for _, tc := range cases {
		if got := det.Match(tc.path); got != tc.want {
			t.Errorf("Match(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestDetector_Detect(t *testing.T) {
	det := compose.New()

	tests := []struct {
		name     string
		content  string
		wantRaws []string
	}{
		{
			name: "single service with bare library image",
			content: `
services:
  web:
    image: nginx:1.25
`,
			wantRaws: []string{"nginx:1.25"},
		},
		{
			name: "multiple services in file order",
			content: `
services:
  web:
    image: ghcr.io/org/web:v1
  db:
    image: postgres:16@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`,
			wantRaws: []string{
				"ghcr.io/org/web:v1",
				"postgres:16@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			},
		},
		{
			name: "tag-less image is reported",
			content: `
services:
  web:
    image: nginx
`,
			wantRaws: []string{"nginx"},
		},
		{
			name: "quoted image",
			content: `
services:
  web:
    image: "localhost:5000/app:dev"
`,
			wantRaws: []string{"localhost:5000/app:dev"},
		},
		{
			name: "interpolated image is kept raw",
			content: `
services:
  web:
    image: ghcr.io/org/web:${TAG:-latest}
`,
			wantRaws: []string{"ghcr.io/org/web:${TAG:-latest}"},
		},
		{
			name: "service without image is skipped",
			content: `
services:
  web:
    build: .
  db:
    image: postgres:16
`,
			wantRaws: []string{"postgres:16"},
		},
		{
			name: "empty image is skipped",
			content: `
services:
  web:
    image:
`,
			wantRaws: nil,
		},
		{
			name: "null image is skipped",
			content: `
services:
  web:
    image: ~
  db:
    image: null
`,
			wantRaws: nil,
		},
		{
			name: "self-referencing merge key terminates",
			content: `
services:
  web: &web
    <<: *web
  db:
    image: postgres:16
`,
			wantRaws: []string{"postgres:16"},
		},
		{
			name: "image keys outside services are ignored",
			content: `
image: top-level:1
x-meta:
  image: extension:1
volumes:
  data:
    image: volume:1
services:
  web:
    image: nginx:1.25
    labels:
      image: label:1
    build:
      args:
        image: build-arg:1
`,
			wantRaws: []string{"nginx:1.25"},
		},
		{
			name: "image inherited through a merge key",
			content: `
x-base: &base
  image: ghcr.io/org/app:v2
  restart: always
services:
  api:
    <<: *base
  worker:
    <<: *base
    command: worker
`,
			wantRaws: []string{"ghcr.io/org/app:v2"},
		},
		{
			name: "own image wins over a merged one",
			content: `
x-base: &base
  image: ghcr.io/org/app:v2
services:
  api:
    <<: *base
    image: ghcr.io/org/api:v3
`,
			wantRaws: []string{"ghcr.io/org/api:v3"},
		},
		{
			name: "merge key with a sequence of anchors",
			content: `
x-logging: &logging
  restart: always
x-base: &base
  image: redis:7
services:
  cache:
    <<: [*logging, *base]
`,
			wantRaws: []string{"redis:7"},
		},
		{
			name: "aliased service",
			content: `
services:
  api: &api
    image: ghcr.io/org/api:v3
  api-canary: *api
`,
			wantRaws: []string{"ghcr.io/org/api:v3"},
		},
		{
			name: "aliased image value",
			content: `
x-image: &image ghcr.io/org/app:v2
services:
  api:
    image: *image
`,
			wantRaws: []string{"ghcr.io/org/app:v2"},
		},
		{
			name: "ignore annotation suppresses the image",
			content: `
services:
  web:
    image: nginx:1.25  # oci-image-detector:ignore
  db:
    image: postgres:16
`,
			wantRaws: []string{"postgres:16"},
		},
		{
			name: "services is not a mapping",
			content: `
services: []
`,
			wantRaws: nil,
		},
		{
			name: "no services key",
			content: `
version: "3.9"
`,
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
			findings, err := det.Detect("compose.yaml", []byte(tc.content))
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
				if f.Strategy != compose.Strategy {
					t.Errorf("finding[%d].Strategy = %q, want %q", i, f.Strategy, compose.Strategy)
				}
				if f.FilePath != "compose.yaml" {
					t.Errorf("finding[%d].FilePath = %q, want %q", i, f.FilePath, "compose.yaml")
				}
			}
		})
	}
}

func TestDetector_Detect_line(t *testing.T) {
	content := "services:\n  web:\n    image: nginx:1.25\n"

	findings, err := compose.New().Detect("compose.yaml", []byte(content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %v", len(findings), findings)
	}
	if findings[0].Line != 3 {
		t.Errorf("Line = %d, want 3", findings[0].Line)
	}
}

func TestDetector_Detect_invalidYAML(t *testing.T) {
	if _, err := compose.New().Detect("compose.yaml", []byte("services: [unterminated")); err == nil {
		t.Error("expected an error for invalid YAML")
	}
}
