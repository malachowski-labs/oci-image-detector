// Package domain contains the core value objects for the oci-image-detector tool.
// It has no dependencies on any infrastructure, framework, or adapter code.
package domain

import "fmt"

// ImageRef is an immutable value object representing a parsed OCI image reference.
//
// Raw is always preserved so no information is lost regardless of whether
// structured parsing succeeded. If parsing fails, only Raw will be populated.
//
// Tag and Digest are not mutually exclusive — a reference of the form
// "name:tag@digest" has both set. When Digest is non-empty, Canonical omits
// the tag so the output is always the most stable identifier.
type ImageRef struct {
	// Raw is the image reference exactly as it was found in the scanned file.
	// Examples: "nginx:1.25", "ghcr.io/org/app@sha256:abc123", "${var.image}"
	Raw string

	// Registry is the hostname of the image registry.
	// Examples: "index.docker.io", "ghcr.io", "123.dkr.ecr.us-east-1.amazonaws.com"
	Registry string

	// Repository is the image path within the registry, without the registry prefix.
	// Examples: "library/nginx", "org/app"
	Repository string

	// Tag is the mutable image tag. May be set alongside Digest for "name:tag@digest" refs.
	// Examples: "latest", "1.25", "main"
	Tag string

	// Digest is the immutable content-addressable identifier.
	// When non-empty, Canonical uses it as the sole version qualifier.
	// Example: "sha256:abc123..."
	Digest string

	// Parsed indicates whether the structured fields were successfully populated.
	// When false, only Raw is reliable.
	Parsed bool
}

// NewImageRef constructs an unresolved ImageRef from a raw string.
// Use this when parsing failed and only the raw reference is available.
func NewImageRef(raw string) ImageRef {
	return ImageRef{Raw: raw}
}

// NewParsedImageRef constructs a fully-populated ImageRef.
// Registry and repository must both be non-empty; this is validated at
// construction time so Canonical never produces a malformed key.
// Pass an empty tag or digest when not applicable.
func NewParsedImageRef(raw, registry, repository, tag, digest string) (ImageRef, error) {
	if registry == "" {
		return ImageRef{}, fmt.Errorf("registry must not be empty (raw: %q)", raw)
	}
	if repository == "" {
		return ImageRef{}, fmt.Errorf("repository must not be empty (raw: %q)", raw)
	}
	return ImageRef{
		Raw:        raw,
		Registry:   registry,
		Repository: repository,
		Tag:        tag,
		Digest:     digest,
		Parsed:     true,
	}, nil
}

// String returns the raw image reference as found in the source file.
func (r ImageRef) String() string {
	return r.Raw
}

// Canonical returns the fully-qualified normalised image reference.
//
// This is the key used for logical deduplication across different notations
// that resolve to the same image. For example:
//   - "nginx"  →  "index.docker.io/library/nginx:latest"
//   - "nginx:latest"  →  "index.docker.io/library/nginx:latest"
//   - "docker.io/library/nginx:latest"  →  "index.docker.io/library/nginx:latest"
//
// When Digest is non-empty it is used as the sole version qualifier; Tag is
// omitted because the digest already uniquely identifies the image layer.
//
// Returns Raw when Parsed is false.
func (r ImageRef) Canonical() string {
	if !r.Parsed {
		return r.Raw
	}

	base := r.Registry + "/" + r.Repository

	if r.Digest != "" {
		return base + "@" + r.Digest
	}

	tag := r.Tag
	if tag == "" {
		tag = "latest"
	}

	return base + ":" + tag
}
