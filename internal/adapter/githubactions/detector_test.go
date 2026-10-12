package githubactions_test

import (
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/githubactions"
)

func TestDetector_Match(t *testing.T) {
	det := &githubactions.Detector{}
	cases := []struct {
		path string
		want bool
	}{
		{".github/workflows/ci.yml", true},
		{".github/workflows/release.yaml", true},
		{".github/actions/setup/action.yml", true},
		{"workflows/ci.yml", false},       // not under .github/
		{".github/dependabot.yml", false}, // not under workflows/ or actions/
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
	det := &githubactions.Detector{}

	tests := []struct {
		name     string
		content  string
		wantRaws []string
	}{
		{
			name: "uses docker:// with bare library image",
			content: `
jobs:
  test:
    steps:
      - uses: docker://alpine:3.12
`,
			wantRaws: []string{"alpine:3.12"},
		},
		{
			name: "uses docker:// with ghcr.io image",
			content: `
jobs:
  build:
    steps:
      - uses: docker://ghcr.io/org/action:v1
`,
			wantRaws: []string{"ghcr.io/org/action:v1"},
		},
		{
			name: "non-docker uses is ignored",
			content: `
jobs:
  test:
    steps:
      - uses: actions/checkout@v4
      - uses: ./.github/actions/setup
`,
			wantRaws: nil,
		},
		{
			name: "empty and null container values name no image",
			content: `
jobs:
  a:
    container:
  b:
    container: ""
  c:
    container: ~
  d:
    container:
      image: null
    services:
      db:
        image:
`,
			wantRaws: nil,
		},
		{
			name: "job container mapping form",
			content: `
jobs:
  test:
    container:
      image: node:18
    steps:
      - run: npm test
`,
			wantRaws: []string{"node:18"},
		},
		{
			name: "job container shorthand form",
			content: `
jobs:
  test:
    container: node:18
    steps:
      - run: npm test
`,
			wantRaws: []string{"node:18"},
		},
		{
			name: "service container image",
			content: `
jobs:
  test:
    services:
      postgres:
        image: postgres:14
    steps:
      - run: echo ok
`,
			wantRaws: []string{"postgres:14"},
		},
		{
			name: "multiple services",
			content: `
jobs:
  test:
    services:
      postgres:
        image: postgres:14
      redis:
        image: redis:7-alpine
    steps:
      - run: echo ok
`,
			wantRaws: []string{"postgres:14", "redis:7-alpine"},
		},
		{
			name: "all three patterns together",
			content: `
jobs:
  test:
    container:
      image: node:18
    services:
      pg:
        image: postgres:14
    steps:
      - uses: docker://alpine:3.12
      - uses: actions/checkout@v4
      - run: echo done
`,
			wantRaws: []string{"node:18", "postgres:14", "alpine:3.12"},
		},
		{
			name: "ignore annotation suppresses uses",
			content: `
jobs:
  test:
    steps:
      - uses: docker://alpine:3.12  # oci-image-detector:ignore
      - uses: docker://ubuntu:22.04
`,
			wantRaws: []string{"ubuntu:22.04"},
		},
		{
			name: "ignore annotation suppresses container image",
			content: `
jobs:
  test:
    container:
      image: node:18  # oci-image-detector:ignore
`,
			wantRaws: nil,
		},
		{
			name: "fully-qualified references outside image fields",
			content: `
env:
  TOOLS_IMAGE: ghcr.io/org/tools:v4
jobs:
  test:
    container: node:18
    steps:
      - run: |
          docker pull quay.io/org/app:v2
          docker pull redis:7
      - uses: org/deploy@v1
        with:
          image: registry.example.com/org/job:v3
          args: ["--base", "gcr.io/org/base:v1"]
`,
			wantRaws: []string{
				"ghcr.io/org/tools:v4",
				"node:18",
				"quay.io/org/app:v2",
				"registry.example.com/org/job:v3",
				"gcr.io/org/base:v1",
			},
		},
		{
			name: "ignore annotation suppresses a line of a run script",
			content: `
jobs:
  test:
    steps:
      - run: |
          docker pull quay.io/org/app:v2  # oci-image-detector:ignore
          docker pull quay.io/org/other:v1
`,
			wantRaws: []string{"quay.io/org/other:v1"},
		},
		{
			name: "docker container action image",
			content: `
runs:
  using: docker
  image: docker://ghcr.io/org/action:v1
`,
			wantRaws: []string{"ghcr.io/org/action:v1"},
		},
		{
			name: "docker container action built from a Dockerfile names no image",
			content: `
runs:
  using: docker
  image: Dockerfile
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
			findings, err := det.Detect(".github/workflows/ci.yml", []byte(tc.content))
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
				if f.Strategy != githubactions.Strategy {
					t.Errorf("finding[%d].Strategy = %q, want %q", i, f.Strategy, githubactions.Strategy)
				}
			}
		})
	}
}

func TestDetector_Detect_line(t *testing.T) {
	content := "jobs:\n  test:\n    steps:\n      - run: |\n          echo start\n          docker pull quay.io/org/app:v2\n"

	findings, err := (&githubactions.Detector{}).Detect(".github/workflows/ci.yml", []byte(content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %v", len(findings), findings)
	}
	if findings[0].Line != 6 {
		t.Errorf("Line = %d, want 6", findings[0].Line)
	}
}
