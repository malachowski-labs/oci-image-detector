package dockerfile_test

import (
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/dockerfile"
)

func TestDetector_Match(t *testing.T) {
	det := dockerfile.New()
	cases := []struct {
		path string
		want bool
	}{
		{"Dockerfile", true},
		{"Dockerfile.dev", true},
		{"app.dockerfile", true},
		{"app.Dockerfile", true},
		{"path/to/Dockerfile", true},
		{"path/to/Dockerfile.prod", true},
		{"main.go", false},
		{"values.yaml", false},
		{"notadockerfile", false},
	}
	for _, tc := range cases {
		if got := det.Match(tc.path); got != tc.want {
			t.Errorf("Match(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestDetector_Detect(t *testing.T) {
	det := dockerfile.New()

	tests := []struct {
		name      string
		content   string
		wantRaws  []string
		wantLines []uint
	}{
		{
			name:      "single FROM",
			content:   "FROM nginx:1.25\nRUN echo hello\n",
			wantRaws:  []string{"nginx:1.25"},
			wantLines: []uint{1},
		},
		{
			name:      "multi-stage build",
			content:   "FROM golang:1.25 AS builder\nFROM alpine:3.19\n",
			wantRaws:  []string{"golang:1.25", "alpine:3.19"},
			wantLines: []uint{1, 2},
		},
		{
			name:      "scratch is skipped",
			content:   "FROM scratch\nFROM alpine:3\n",
			wantRaws:  []string{"alpine:3"},
			wantLines: []uint{2},
		},
		{
			name:      "platform flag ignored",
			content:   "FROM --platform=linux/amd64 nginx:latest\n",
			wantRaws:  []string{"nginx:latest"},
			wantLines: []uint{1},
		},
		{
			name:      "template variable kept as raw",
			content:   "FROM $BASE_IMAGE\n",
			wantRaws:  []string{"$BASE_IMAGE"},
			wantLines: []uint{1},
		},
		{
			name:      "no FROM instructions",
			content:   "RUN echo hello\nCOPY . .\n",
			wantRaws:  nil,
			wantLines: nil,
		},
		{
			name:      "line continuation in FROM",
			content:   "FROM \\\n  nginx:1.25\n",
			wantRaws:  []string{"nginx:1.25"},
			wantLines: []uint{1},
		},
		{
			name:      "comment and blank lines are ignored",
			content:   "# syntax=docker/dockerfile:1\n\nFROM alpine:3.19\n",
			wantRaws:  []string{"alpine:3.19"},
			wantLines: []uint{3},
		},
		{
			name:      "global ARG expanded into FROM",
			content:   "ARG VERSION=1.25\nFROM nginx:${VERSION}\n",
			wantRaws:  []string{"nginx:1.25"},
			wantLines: []uint{2},
		},
		{
			name:      "ARG without default stays raw",
			content:   "ARG BASE\nFROM ${BASE}\n",
			wantRaws:  []string{"${BASE}"},
			wantLines: []uint{2},
		},
		{
			name:      "FROM referencing a prior stage is skipped",
			content:   "FROM golang:1.25 AS builder\nFROM builder\nRUN go build\n",
			wantRaws:  []string{"golang:1.25"},
			wantLines: []uint{1},
		},
		{
			name:      "COPY --from external image is reported",
			content:   "FROM alpine:3.19\nCOPY --from=nginx:1.25 /etc/nginx /etc/nginx\n",
			wantRaws:  []string{"alpine:3.19", "nginx:1.25"},
			wantLines: []uint{1, 2},
		},
		{
			name:      "COPY --from stage reference is skipped",
			content:   "FROM golang:1.25 AS builder\nFROM alpine:3.19\nCOPY --from=builder /app /app\nCOPY --from=0 /x /y\n",
			wantRaws:  []string{"golang:1.25", "alpine:3.19"},
			wantLines: []uint{1, 2},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := det.Detect("Dockerfile", []byte(tc.content))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(findings) != len(tc.wantRaws) {
				t.Fatalf("got %d findings, want %d", len(findings), len(tc.wantRaws))
			}
			for i, f := range findings {
				if f.Ref.Raw != tc.wantRaws[i] {
					t.Errorf("finding[%d].Raw = %q, want %q", i, f.Ref.Raw, tc.wantRaws[i])
				}
				if f.Line != tc.wantLines[i] {
					t.Errorf("finding[%d].Line = %d, want %d", i, f.Line, tc.wantLines[i])
				}
				if f.FilePath != "Dockerfile" {
					t.Errorf("finding[%d].FilePath = %q, want %q", i, f.FilePath, "Dockerfile")
				}
				if f.Strategy != dockerfile.Strategy {
					t.Errorf("finding[%d].Strategy = %q, want %q", i, f.Strategy, dockerfile.Strategy)
				}
			}
		})
	}
}
