package cmd

import (
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/port"
	"github.com/malachowski-labs/oci-image-detector/internal/registry"
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
