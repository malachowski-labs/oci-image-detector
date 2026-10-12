package cmd

import (
	"slices"
	"strings"
	"testing"

	"github.com/malachowski-labs/oci-image-detector/pkg/port"
	"github.com/malachowski-labs/oci-image-detector/pkg/registry"
)

func TestBuiltinDetectors_fallbackIsConsultedLast(t *testing.T) {
	dets := registry.Default().Detectors()
	if len(dets) == 0 {
		t.Fatal("expected at least one file-level detector")
	}

	seenFallback := false
	for _, d := range dets {
		switch d.Tier() {
		case port.TierFallback:
			seenFallback = true
		case port.TierSpecialist:
			if seenFallback {
				t.Errorf("specialist %q is ordered after a fallback detector", d.Name())
			}
		}
	}
	if !seenFallback {
		t.Error("expected a fallback detector to be registered")
	}
}

func TestBuiltinDetectors_registersDirDetectors(t *testing.T) {
	if len(registry.Default().DirDetectors()) == 0 {
		t.Error("expected at least one directory-aware detector")
	}
}

// Self-registration fails silently: dropping a blank import from detectors.go
// removes a detector without a compile error. Pinning the exact set turns that
// into a test failure.
func TestBuiltinDetectors_exactSet(t *testing.T) {
	var files []string
	for _, d := range registry.Default().Detectors() {
		files = append(files, d.Name())
	}
	if want := []string{"dockerfile", "githubactions", "helm", "generic"}; !slices.Equal(files, want) {
		t.Errorf("file-level detectors = %v, want %v", files, want)
	}

	var dirs []string
	for _, d := range registry.Default().DirDetectors() {
		dirs = append(dirs, d.Name())
	}
	if want := []string{"terraform"}; !slices.Equal(dirs, want) {
		t.Errorf("directory-aware detectors = %v, want %v", dirs, want)
	}
}

// Specialists must claim disjoint sets of files. Name order within a tier only
// makes the result deterministic; it must never be what decides which
// specialist owns a file.
func TestBuiltinDetectors_specialistsDoNotOverlap(t *testing.T) {
	tests := []struct {
		path string
		want string // owning specialist; empty when the file is left to the fallback
	}{
		{path: "Dockerfile", want: "dockerfile"},
		{path: "build/Dockerfile.prod", want: "dockerfile"},
		{path: "build/app.dockerfile", want: "dockerfile"},
		{path: "values.yaml", want: "helm"},
		{path: "charts/app/values.yml", want: "helm"},
		{path: ".github/workflows/ci.yml", want: "githubactions"},
		{path: ".github/actions/build/action.yaml", want: "githubactions"},
		// GitHub treats these as workflows and actions regardless of their name.
		{path: ".github/workflows/values.yaml", want: "githubactions"},
		{path: ".github/actions/build/values.yml", want: "githubactions"},
		{path: ".github/workflows/Dockerfile", want: "dockerfile"},
		{path: "deploy/app.yaml", want: ""},
		{path: "README.md", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			var owners []string
			for _, d := range registry.Default().Detectors() {
				if d.Tier() == port.TierSpecialist && d.Match(tt.path) {
					owners = append(owners, d.Name())
				}
			}

			if len(owners) > 1 {
				t.Fatalf("specialists %v all claim the file", owners)
			}
			if got := strings.Join(owners, ""); got != tt.want {
				t.Errorf("owning specialist = %q, want %q", got, tt.want)
			}
		})
	}
}
