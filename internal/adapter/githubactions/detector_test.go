package githubactions_test

import (
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/githubactions"
)

func TestDetector_Match(t *testing.T) {
	det := githubactions.New()
	cases := []struct {
		path string
		want bool
	}{
		{".github/workflows/ci.yml", true},
		{".github/workflows/release.yaml", true},
		{".github/actions/setup/action.yml", true},
		{"workflows/ci.yml", false},        // not under .github/
		{".github/dependabot.yml", false},  // not under workflows/ or actions/
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
	det := githubactions.New()

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
