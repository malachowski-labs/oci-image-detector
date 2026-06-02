// Package imageref provides shared helpers for parsing and classifying OCI
// image reference strings using go-containerregistry.
package imageref

import (
	"strings"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// Parse attempts to parse raw as an OCI image reference.
// On success it returns a fully-populated domain.ImageRef with Parsed=true.
// On failure (e.g. template variables, invalid syntax) it returns an unparsed
// ImageRef containing only the Raw field.
func Parse(raw string) domain.ImageRef {
	// Reject strings containing template placeholders — they cannot be resolved
	// at scan time and must be kept as raw-only findings.
	if containsTemplate(raw) {
		return domain.NewImageRef(raw)
	}

	ref, err := name.ParseReference(raw, name.WeakValidation)
	if err != nil {
		return domain.NewImageRef(raw)
	}

	registry := ref.Context().RegistryStr()
	repository := ref.Context().RepositoryStr()

	var tag, digest string
	switch r := ref.(type) {
	case name.Tag:
		tag = r.TagStr()
	case name.Digest:
		digest = r.DigestStr()
	}

	parsed, err := domain.NewParsedImageRef(raw, registry, repository, tag, digest)
	if err != nil {
		// Registry or repository were empty after parsing — return raw only.
		return domain.NewImageRef(raw)
	}

	return parsed
}

// LooksLikeImage reports whether ref is likely an OCI image reference worth
// reporting. Detectors use this to filter false positives after calling Parse:
//
//   - Parsed refs: require "/" or ":" in the raw string so that plain words
//     (e.g. "enabled", "true") that go-containerregistry silently accepts as
//     index.docker.io/library/<word>:latest are not emitted as findings.
//   - Unparsed refs (unresolvable template placeholders): require a version
//     qualifier (":" or "@sha256:") because an expression without a version
//     component is unlikely to identify a specific image.
func LooksLikeImage(ref domain.ImageRef) bool {
	if ref.Parsed {
		return strings.ContainsAny(ref.Raw, "/:") || strings.Contains(ref.Raw, "@sha256:")
	}
	// Unparsed path: only version-qualified template refs are emitted.
	return strings.Contains(ref.Raw, ":") || strings.Contains(ref.Raw, "@sha256:")
}

// containsTemplate reports whether s contains a known template placeholder
// that cannot be resolved at scan time. Each check is annotated with the
// template syntax it targets.
func containsTemplate(s string) bool {
	return strings.Contains(s, "$") || // shell: $VAR, ${VAR}; Terraform: ${var.x}; K8s: $(VAR)
		strings.Contains(s, "{{") || // Go/Helm templates: {{ .Values.tag }}
		strings.Contains(s, "%(") || // Python %-format strings: %(key)s
		strings.Contains(s, "#{") // Ruby string interpolation: #{expr}
}
