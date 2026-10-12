package yamlscan_test

import (
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/malachowski-labs/oci-image-detector/internal/yamlscan"
	"github.com/malachowski-labs/oci-image-detector/pkg/domain"
)

// valueOf parses a one-key document and returns the node of its value.
func valueOf(t *testing.T, content string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return doc.Content[0].Content[1]
}

func TestHasValue(t *testing.T) {
	cases := []struct {
		content string
		want    bool
	}{
		{"key: nginx", true},
		{"key: 7", true},
		{"key:", false},
		{`key: ""`, false},
		{"key: ~", false},
		{"key: null", false},
		{"key: [a]", false},
		{"key: {a: b}", false},
	}
	for _, tc := range cases {
		if got := yamlscan.HasValue(valueOf(t, tc.content)); got != tc.want {
			t.Errorf("HasValue(%q) = %v, want %v", tc.content, got, tc.want)
		}
	}
}

func TestScalar(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantRaws  []string
		wantLines []int
	}{
		{
			name:      "fully-qualified reference",
			content:   "key: ghcr.io/org/app:v1",
			wantRaws:  []string{"ghcr.io/org/app:v1"},
			wantLines: []int{1},
		},
		{
			name:      "reference inside other text",
			content:   `key: "--image=quay.io/org/tool:v2 --verbose"`,
			wantRaws:  []string{"quay.io/org/tool:v2"},
			wantLines: []int{1},
		},
		{
			name:    "short forms are not candidates",
			content: "key: nginx:1.25",
		},
		{
			name:    "ignore annotation on the value",
			content: "key: ghcr.io/org/app:v1  # oci-image-detector:ignore",
		},
		{
			name:      "literal block is attributed line by line",
			content:   "key: |\n  pull ghcr.io/org/a:v1\n  pull ghcr.io/org/b:v1  # oci-image-detector:ignore\n  pull ghcr.io/org/c:v1\n",
			wantRaws:  []string{"ghcr.io/org/a:v1", "ghcr.io/org/c:v1"},
			wantLines: []int{2, 4},
		},
		{
			name:      "folded block is attributed to its first line",
			content:   "key: >\n  pull ghcr.io/org/a:v1\n  pull ghcr.io/org/b:v1\n",
			wantRaws:  []string{"ghcr.io/org/a:v1", "ghcr.io/org/b:v1"},
			wantLines: []int{1, 1},
		},
		{
			name:    "a mapping holds no text",
			content: "key: {nested: ghcr.io/org/app:v1}",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var raws []string
			var lines []int
			yamlscan.Scalar(valueOf(t, tc.content), func(ref domain.ImageRef, line int) {
				raws = append(raws, ref.Raw)
				lines = append(lines, line)
			})
			if !slices.Equal(raws, tc.wantRaws) {
				t.Errorf("raws = %q, want %q", raws, tc.wantRaws)
			}
			if !slices.Equal(lines, tc.wantLines) {
				t.Errorf("lines = %v, want %v", lines, tc.wantLines)
			}
		})
	}
}
