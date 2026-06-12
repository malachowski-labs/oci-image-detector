// Package helm provides a Detector for Helm values files.
package helm

import (
	"fmt"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/imageref"
	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// Strategy is the stable identifier for this detection strategy.
const Strategy domain.Strategy = "helm"

// Detector implements port.Detector for Helm values files.
type Detector struct{}

// New returns a new Helm Detector.
func New() *Detector { return &Detector{} }

// Name implements port.Detector.
func (d *Detector) Name() string { return string(Strategy) }

// Match implements port.Detector.
// Returns true for values.yaml and values.yml at any depth in the tree.
func (d *Detector) Match(filePath string) bool {
	lower := strings.ToLower(path.Base(filePath))
	return lower == "values.yaml" || lower == "values.yml"
}

// Detect implements port.Detector.
//
// It walks the YAML document tree looking for image blocks: mapping nodes that
// (a) sit under an image-shaped parent key (e.g. "image", "mainImage"), and
// (b) carry a "repository" plus a pinning qualifier (tag or digest). The
// assembled raw is then validated with `imageref.Parse` so any non-image
// string that survives the structural checks (a `repository: https://…`
// literal, a free-form description, …) is dropped.
//
// The Bitnami `image: { registry?, repository, tag, digest? }` convention is
// the canonical shape this detector targets. Other conventions that use a
// `repository` key for non-image data (OCM repository mappings, rescoring
// rulesets that point at Git URLs, etc.) are intentionally ignored.
func (d *Detector) Detect(filePath string, content []byte) ([]domain.Finding, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("helm: parse %q: %w", filePath, err)
	}
	if root.Kind == 0 {
		return nil, nil // empty file
	}

	var findings []domain.Finding
	walkNode(&root, "", filePath, &findings)
	return findings, nil
}

// walkNode recursively traverses a yaml.Node looking for image map nodes.
//
// parentKey is the mapping key under which node sits, or "" at the document
// root. Sequence elements inherit their containing sequence's parent key so
// that `images: [{repository, tag}, …]` patterns are also recognised.
//
// Limitation: when a mapping node is identified as an image block, the node is
// consumed and its children are not recursed into. A mapping that has both a
// "repository" key and nested image sub-maps will therefore only produce one
// finding for the outermost block.
func walkNode(node *yaml.Node, parentKey, filePath string, findings *[]domain.Finding) {
	if node.Kind == yaml.DocumentNode {
		for _, child := range node.Content {
			walkNode(child, "", filePath, findings)
		}
		return
	}

	if node.Kind == yaml.MappingNode {
		// Only attempt image-block extraction when the parent key looks like
		// an image attribute. Without this gate, any mapping that happens to
		// carry a `repository` key (OCM repo mappings, rescoring-config
		// references, registry endpoint blocks, …) is mis-reported.
		if isImageParent(parentKey) {
			if f, ok := imageFinding(node, filePath); ok {
				*findings = append(*findings, f)
				// Don't recurse into a consumed image block.
				return
			}
		}
		// Recurse into values of this map, threading each key through to the
		// child so it can decide whether it is an image block.
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			walkNode(node.Content[i+1], key, filePath, findings)
		}
		return
	}

	if node.Kind == yaml.SequenceNode {
		// Sequence elements inherit the parent key (e.g. `images: [{…}, {…}]`
		// — each element should be evaluated as if its parent were "images").
		for _, child := range node.Content {
			walkNode(child, parentKey, filePath, findings)
		}
	}
}

// imageParentSuffixes are the case-insensitive suffixes that mark a mapping
// key as an image attribute. Both singular and plural forms are accepted —
// the plural covers the common `images: [{repository, tag}, …]` collection
// pattern whose elements inherit the parent key. An optional hyphen or
// underscore separator covers kebab- and snake-cased variants
// (`init-image`, `metrics_image`, `sidecar-images`).
//
// Matching is "exact key OR ends-with suffix", which lets `mainImage`,
// `extraImages`, etc. through while still rejecting unrelated keys.
var imageParentSuffixes = []string{
	"image", "images",
	"-image", "-images",
	"_image", "_images",
}

// isImageParent reports whether key looks like a Helm image attribute name.
func isImageParent(key string) bool {
	if key == "" {
		return false
	}
	lower := strings.ToLower(key)
	for _, suffix := range imageParentSuffixes {
		if lower == suffix || strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// imageFinding extracts a finding from a mapping node already known to sit in
// image position. ok=false drops the candidate entirely; callers fall back to
// recursing into the mapping's children.
//
// A mapping qualifies as an image block only if it carries a "repository" key
// AND a pinning qualifier — a non-empty "tag" or "digest". Tag-less references
// are intentionally rejected: the consumers of this tool need scannable
// (i.e. version-pinned) image references, and a registry path on its own is
// almost always non-image config (an OCM repository, a registry endpoint, …).
//
// `repository` values that contain `://` are treated as URL literals (Git
// repos, Helm `oci://` chart references, https endpoints) and rejected
// up-front. go-containerregistry's WeakValidation otherwise accepts strings
// like `https://example.com/org/app:v1` as references because the trailing
// `:v1` matches its tag grammar — the structural parent gate is not enough
// to keep those out.
//
// Once assembled, the raw is validated with `imageref.Parse`:
//
//   - Parsed=true means go-containerregistry recognised the string as a
//     reference; we trust the surrounding structural context (image-shaped
//     parent + tag/digest sibling) to vouch for it. This keeps Bitnami short
//     forms like `repository: nginx, tag: "1.25"` working even though their
//     registry host defaults to docker.io.
//   - Parsed=false is preserved only when the raw contains a template
//     placeholder (`{{ .Values.imageTag }}`, `${var}`). Those are legitimate
//     findings — the version is unresolved at scan time. Any other parse
//     failure is dropped.
func imageFinding(node *yaml.Node, filePath string) (domain.Finding, bool) {
	repo, repoLine := mapValue(node, "repository")
	if repo == "" || strings.Contains(repo, "://") {
		return domain.Finding{}, false
	}
	registry, _ := mapValue(node, "registry")
	if strings.Contains(registry, "://") {
		return domain.Finding{}, false
	}
	tag, _ := mapValue(node, "tag")
	digest, _ := mapValue(node, "digest")
	if tag == "" && digest == "" {
		return domain.Finding{}, false
	}

	raw := buildRaw(registry, repo, tag, digest)
	ref := imageref.Parse(raw)

	if !ref.Parsed && !containsTemplate(raw) {
		return domain.Finding{}, false
	}

	return domain.Finding{
		Ref:      ref,
		FilePath: filePath,
		Line:     uint(repoLine),
		Strategy: Strategy,
	}, true
}

// containsTemplate reports whether s contains a known template placeholder
// that could explain why imageref.Parse marked the ref unparsed. Mirrors the
// list in imageref.containsTemplate; kept local to avoid widening that
// package's API for a single caller.
func containsTemplate(s string) bool {
	return strings.Contains(s, "$") ||
		strings.Contains(s, "{{") ||
		strings.Contains(s, "%(") ||
		strings.Contains(s, "#{")
}

// mapValue returns the string value and line number for key in a MappingNode.
func mapValue(node *yaml.Node, key string) (string, int) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1].Value, node.Content[i].Line
		}
	}
	return "", 0
}

// buildRaw combines an optional registry, a repository, and an optional tag
// and/or digest into a raw image reference string. The output mirrors the
// Bitnami three-key convention: "<registry>/<repo>:<tag>@<digest>". Tag and
// digest may both be present (the canonical form keeps both); when only one
// is set, the other is omitted.
func buildRaw(registry, repo, tag, digest string) string {
	var sb strings.Builder
	if registry != "" {
		sb.WriteString(registry)
		sb.WriteByte('/')
	}
	sb.WriteString(repo)
	if tag != "" {
		sb.WriteByte(':')
		sb.WriteString(tag)
	}
	if digest != "" {
		sb.WriteByte('@')
		sb.WriteString(digest)
	}
	return sb.String()
}
