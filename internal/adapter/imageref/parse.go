// Package imageref provides shared helpers for parsing and classifying OCI
// image reference strings using go-containerregistry.
package imageref

import (
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// candidateRe matches strings that are likely OCI image references.
//
// It is the shared precision filter for detectors that scan unstructured text
// (the generic fallback) or every line of a file (the Terraform adapter),
// where almost any "word/word" or "word:word" token would otherwise look like
// an image. To keep precision high it matches only fully-qualified
// references and deliberately ignores ambiguous short forms — bare library
// images ("nginx:1.25"), single-namespace paths ("org/repo"), fractions
// ("0/2"), file paths ("docs/overview.md") and config pairs ("language:go").
// Those short forms are the job of detectors with strong structural context
// (a Dockerfile FROM line, a Helm/Kubernetes "image:" key) to resolve safely.
//
// A match therefore requires both:
//
//  1. An identifiable registry host — a dotted domain (ghcr.io, gcr.io,
//     123.dkr.ecr.us-east-1.amazonaws.com) or an explicit host:port
//     (localhost:5000). A plain word is never treated as a registry.
//  2. A mandatory tag or digest. The tag is anchored to end on an alphanumeric
//     so trailing sentence punctuation is not captured (RE2 has no lookahead),
//     e.g. "deploy ghcr.io/org/app:v1." yields "ghcr.io/org/app:v1".
var candidateRe = regexp.MustCompile(
	`(?:` +
		// registry host: dotted domain (optional :port) or host:port.
		`(?:` +
		`[a-z0-9]+(?:[.-][a-z0-9]+)*\.[a-z0-9]+(?:[.-][a-z0-9]+)*(?::[0-9]+)?` +
		`|[a-z0-9]+(?:[.-][a-z0-9]+)*:[0-9]+` +
		`)` +
		// repository path: one or more "/component" segments.
		`(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+` +
		// mandatory tag (optionally followed by a digest) or a bare digest.
		`(?::[a-zA-Z0-9](?:[a-zA-Z0-9._-]*[a-zA-Z0-9])?(?:@sha256:[a-fA-F0-9]+)?` +
		`|@sha256:[a-fA-F0-9]+)` +
		`)`,
)

// Candidates returns every substring of s with the structural shape of a
// fully-qualified OCI image reference (registry host + repository path +
// mandatory tag or digest). Callers should still pass each result through
// Parse to obtain a validated domain.ImageRef.
//
// Matches immediately preceded by "/" are dropped: a registry host never
// legitimately follows a slash, so such a match is the tail of a URI or path
// (e.g. the "iam.googleapis.com/pool/attribute.x:value" inside a
// "principalSet://…" WIF principal, or any "scheme://host/path:tag" URL)
// rather than a standalone image reference.
func Candidates(s string) []string {
	var out []string
	for _, loc := range candidateRe.FindAllStringIndex(s, -1) {
		if loc[0] > 0 && s[loc[0]-1] == '/' {
			continue
		}
		out = append(out, s[loc[0]:loc[1]])
	}
	return out
}

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
