// Package imageref provides shared helpers for parsing and classifying OCI
// image reference strings using go-containerregistry.
package imageref

import (
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/malachowski-labs/oci-image-detector/pkg/domain"
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
	if ContainsTemplate(raw) {
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
		// go-containerregistry resolves "name:tag@digest" to a name.Digest and
		// discards the tag. Recover it from the raw string so both fields are
		// populated, per the domain.ImageRef contract; Canonical still emits the
		// digest alone.
		tag = tagFromTaggedDigest(raw)
	}

	return domain.NewParsedImageRef(raw, registry, repository, tag, digest)
}

// tagFromTaggedDigest extracts the tag from a "name:tag@digest" reference, or
// returns "" when no tag is present. The tag, if any, follows the last ':' that
// comes after the last '/'; an earlier colon belongs to a registry host:port.
// raw is assumed to have already parsed as a valid digest reference, so the
// extracted tag needs no further validation.
func tagFromTaggedDigest(raw string) string {
	base, _, ok := strings.Cut(raw, "@")
	if !ok {
		return ""
	}
	if colon := strings.LastIndex(base, ":"); colon > strings.LastIndex(base, "/") {
		return base[colon+1:]
	}
	return ""
}

// ContainsTemplate reports whether s contains a known template placeholder
// that cannot be resolved at scan time. Parse returns such a string unparsed;
// detectors use this to tell that case from a string that is not a reference.
// Each check is annotated with the template syntax it targets.
func ContainsTemplate(s string) bool {
	return strings.Contains(s, "$") || // shell: $VAR, ${VAR}; Terraform: ${var.x}; K8s: $(VAR)
		strings.Contains(s, "{{") || // Go/Helm templates: {{ .Values.tag }}
		strings.Contains(s, "%(") || // Python %-format strings: %(key)s
		strings.Contains(s, "#{") // Ruby string interpolation: #{expr}
}

// sourceFileExtensions are file extensions that identify a source-code or
// markup file. An OCI repository path never ends in one of these, so a
// candidate whose final path segment carries such an extension is a file
// reference (e.g. a Go stack trace "github.com/org/repo/main.go:47", where the
// ":47" is a line number, not a tag) rather than an image reference.
//
// A numeric-tag filter would also reject those stack traces, but it would
// wrongly drop legitimate host-qualified images with integer tags such as
// "gcr.io/proj/redis:7" — so we key off the source-file extension instead.
var sourceFileExtensions = map[string]bool{
	"go": true, "py": true, "js": true, "ts": true, "jsx": true, "tsx": true,
	"rb": true, "java": true, "rs": true, "c": true, "cc": true, "cpp": true,
	"cxx": true, "h": true, "hpp": true, "cs": true, "php": true, "kt": true,
	"kts": true, "swift": true, "scala": true, "sh": true, "bash": true,
	"pl": true, "pm": true, "lua": true, "ex": true, "exs": true, "erl": true,
	"clj": true, "dart": true, "groovy": true, "m": true, "mm": true,
}

// IsSourceFile reports whether ref's repository path points at a source-code
// file (its final path segment ends in a known source-file extension) rather
// than at an image. Detectors that scan text with Candidates use it to drop
// file:line references that happen to match the image-reference shape.
func IsSourceFile(ref domain.ImageRef) bool {
	repo := ref.Repository
	if repo == "" {
		repo = ref.Raw
	}
	last := repo
	if i := strings.LastIndexByte(repo, '/'); i >= 0 {
		last = repo[i+1:]
	}
	dot := strings.LastIndexByte(last, '.')
	if dot < 0 {
		return false
	}
	return sourceFileExtensions[last[dot+1:]]
}
