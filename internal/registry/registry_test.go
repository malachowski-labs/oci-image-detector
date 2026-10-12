package registry_test

import (
	"errors"
	"io/fs"
	"slices"
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/domain"
	"github.com/malachowski-labs/oci-image-detector/internal/port"
	"github.com/malachowski-labs/oci-image-detector/internal/registry"
)

// --- test doubles ---

type stubDetector struct {
	name string
	tier port.Tier
}

func (d *stubDetector) Name() string      { return d.name }
func (d *stubDetector) Tier() port.Tier   { return d.tier }
func (d *stubDetector) Match(string) bool { return true }
func (d *stubDetector) Detect(string, []byte) ([]domain.Finding, error) {
	return nil, nil
}

type stubDirDetector struct{ name string }

func (d *stubDirDetector) Name() string                        { return d.name }
func (d *stubDirDetector) MatchDir(string, []fs.DirEntry) bool { return true }
func (d *stubDirDetector) DetectDir(fs.FS) ([]domain.Finding, error) {
	return nil, nil
}

// --- helpers ---

func specialist(name string) *stubDetector {
	return &stubDetector{name: name, tier: port.TierSpecialist}
}

func fallback(name string) *stubDetector {
	return &stubDetector{name: name, tier: port.TierFallback}
}

func names(dets []port.Detector) []string {
	out := make([]string, len(dets))
	for i, d := range dets {
		out[i] = d.Name()
	}
	return out
}

// --- tests ---

func TestDetectors_empty(t *testing.T) {
	r := registry.New()
	if got := r.Detectors(); len(got) != 0 {
		t.Errorf("expected no detectors, got %d", len(got))
	}
	if got := r.DirDetectors(); len(got) != 0 {
		t.Errorf("expected no dir detectors, got %d", len(got))
	}
}

func TestDetectors_specialistsBeforeFallbacks(t *testing.T) {
	r := registry.New()
	// Registered in the worst possible order on purpose: the result must not
	// depend on it.
	for _, d := range []port.Detector{fallback("a-fallback"), specialist("z-specialist"), specialist("m-specialist")} {
		if err := r.Register(d); err != nil {
			t.Fatalf("Register(%q): unexpected error: %v", d.Name(), err)
		}
	}

	want := []string{"m-specialist", "z-specialist", "a-fallback"}
	if got := names(r.Detectors()); !slices.Equal(got, want) {
		t.Errorf("Detectors() order = %v, want %v", got, want)
	}
}

func TestDetectors_returnsCopy(t *testing.T) {
	r := registry.New()
	if err := r.Register(specialist("a")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	r.Detectors()[0] = specialist("mutated")

	if got := r.Detectors()[0].Name(); got != "a" {
		t.Errorf("registry was mutated through the returned slice: got %q, want %q", got, "a")
	}
}

func TestDirDetectors_orderedByName(t *testing.T) {
	r := registry.New()
	for _, name := range []string{"b", "a"} {
		if err := r.RegisterDir(&stubDirDetector{name: name}); err != nil {
			t.Fatalf("RegisterDir(%q): unexpected error: %v", name, err)
		}
	}

	got := r.DirDetectors()
	if len(got) != 2 || got[0].Name() != "a" || got[1].Name() != "b" {
		t.Errorf("DirDetectors() not ordered by name: got %d detectors", len(got))
	}
}

func TestRegister_rejectsInvalidDetectors(t *testing.T) {
	tests := []struct {
		name string
		det  port.Detector
		want error
	}{
		{name: "nil", det: nil, want: registry.ErrNilDetector},
		{name: "empty name", det: specialist(""), want: registry.ErrEmptyName},
		{name: "duplicate name", det: specialist("taken"), want: registry.ErrDuplicateName},
		{name: "unknown tier", det: &stubDetector{name: "odd", tier: port.Tier(99)}, want: registry.ErrUnknownTier},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := registry.New()
			if err := r.Register(fallback("taken")); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if err := r.Register(tt.det); !errors.Is(err, tt.want) {
				t.Errorf("Register() error = %v, want %v", err, tt.want)
			}
			if got := len(r.Detectors()); got != 1 {
				t.Errorf("rejected detector was registered: got %d detectors, want 1", got)
			}
		})
	}
}

func TestRegisterDir_rejectsInvalidDetectors(t *testing.T) {
	tests := []struct {
		name string
		det  port.DirectoryAwareDetector
		want error
	}{
		{name: "nil", det: nil, want: registry.ErrNilDetector},
		{name: "empty name", det: &stubDirDetector{}, want: registry.ErrEmptyName},
		// Names are shared with file-level detectors: both end up as Finding.Strategy.
		{name: "name taken by file detector", det: &stubDirDetector{name: "taken"}, want: registry.ErrDuplicateName},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := registry.New()
			if err := r.Register(specialist("taken")); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if err := r.RegisterDir(tt.det); !errors.Is(err, tt.want) {
				t.Errorf("RegisterDir() error = %v, want %v", err, tt.want)
			}
			if got := len(r.DirDetectors()); got != 0 {
				t.Errorf("rejected detector was registered: got %d dir detectors, want 0", got)
			}
		})
	}
}
