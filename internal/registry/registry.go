// Package registry collects the detectors available to a scan and hands them
// to the ScanService in a deterministic, declared order.
//
// It depends only on internal/port — never on adapters. Detectors register
// themselves with the default registry from their init functions, so linking a
// detector package into the binary is all it takes to enable it; the registry
// decides in which order detectors are consulted.
package registry

import (
	"cmp"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/malachowski-labs/oci-image-detector/internal/port"
)

// ErrNilDetector is returned when a nil detector is registered.
var ErrNilDetector = errors.New("registry: detector is nil")

// ErrEmptyName is returned when a detector reports an empty Name.
var ErrEmptyName = errors.New("registry: detector name is empty")

// ErrDuplicateName is returned when two detectors report the same Name.
var ErrDuplicateName = errors.New("registry: duplicate detector name")

// ErrUnknownTier is returned when a detector reports a Tier the registry does
// not know how to order.
var ErrUnknownTier = errors.New("registry: unknown detector tier")

// Registry holds the registered detectors. The zero value is not usable; build
// one with New. A Registry is not safe for concurrent use.
type Registry struct {
	detectors    []port.Detector
	dirDetectors []port.DirectoryAwareDetector
	names        map[string]struct{}
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{names: make(map[string]struct{})}
}

// defaultRegistry is the process-wide registry that detector packages register
// into from init. It is only written during package initialisation, which the
// Go runtime runs sequentially, and only read afterwards.
var defaultRegistry = New()

// Default returns the registry holding every self-registered detector.
func Default() *Registry { return defaultRegistry }

// MustRegister adds a file-level detector to the default registry.
// It is meant to be called from a detector package's init function and panics
// on the errors Register reports: a misconfigured detector is a programming
// error that must stop the binary before any scan starts.
func MustRegister(d port.Detector) {
	if err := defaultRegistry.Register(d); err != nil {
		panic(err)
	}
}

// MustRegisterDir adds a directory-aware detector to the default registry.
// Same contract as MustRegister.
func MustRegisterDir(d port.DirectoryAwareDetector) {
	if err := defaultRegistry.RegisterDir(d); err != nil {
		panic(err)
	}
}

// Register adds a file-level detector.
// It rejects a nil detector, an empty or already registered name, and an
// unknown tier, so a misconfigured detector fails at start-up rather than
// silently shadowing another one mid-scan.
func (r *Registry) Register(d port.Detector) error {
	if isNil(d) {
		return ErrNilDetector
	}
	if t := d.Tier(); !t.Valid() {
		return fmt.Errorf("%w: %q declares %s", ErrUnknownTier, d.Name(), t)
	}
	if err := r.claimName(d.Name()); err != nil {
		return err
	}
	r.detectors = append(r.detectors, d)
	return nil
}

// RegisterDir adds a directory-aware detector.
// Names are unique across both kinds of detector because a name becomes
// Finding.Strategy regardless of which kind produced the finding.
func (r *Registry) RegisterDir(d port.DirectoryAwareDetector) error {
	if isNil(d) {
		return ErrNilDetector
	}
	if err := r.claimName(d.Name()); err != nil {
		return err
	}
	r.dirDetectors = append(r.dirDetectors, d)
	return nil
}

// Detectors returns the file-level detectors in the order the ScanService must
// consult them: every specialist before any fallback, and by name within a
// tier. Registration order has no influence on the result.
func (r *Registry) Detectors() []port.Detector {
	out := slices.Clone(r.detectors)
	slices.SortFunc(out, func(a, b port.Detector) int {
		return cmp.Or(
			cmp.Compare(a.Tier(), b.Tier()),
			cmp.Compare(a.Name(), b.Name()),
		)
	})
	return out
}

// DirDetectors returns the directory-aware detectors ordered by name.
func (r *Registry) DirDetectors() []port.DirectoryAwareDetector {
	out := slices.Clone(r.dirDetectors)
	slices.SortFunc(out, func(a, b port.DirectoryAwareDetector) int {
		return cmp.Compare(a.Name(), b.Name())
	})
	return out
}

// isNil reports whether d is a nil interface or an interface holding a nil
// value, such as a nil *Detector. A plain d == nil misses the latter.
func isNil(d any) bool {
	if d == nil {
		return true
	}
	switch v := reflect.ValueOf(d); v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return v.IsNil()
	default:
		return false
	}
}

// claimName reserves name, failing when it is empty or already taken.
func (r *Registry) claimName(name string) error {
	if name == "" {
		return ErrEmptyName
	}
	if _, taken := r.names[name]; taken {
		return fmt.Errorf("%w: %q", ErrDuplicateName, name)
	}
	r.names[name] = struct{}{}
	return nil
}
