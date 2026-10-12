// Package helm provides a Detector for Helm values files.
package helm

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/malachowski-labs/oci-image-detector/internal/githubpath"
	"github.com/malachowski-labs/oci-image-detector/internal/yamlscan"
	"github.com/malachowski-labs/oci-image-detector/pkg/domain"
	"github.com/malachowski-labs/oci-image-detector/pkg/imageref"
	"github.com/malachowski-labs/oci-image-detector/pkg/port"
	"github.com/malachowski-labs/oci-image-detector/pkg/registry"
	"github.com/malachowski-labs/oci-image-detector/pkg/suppress"
)

// Strategy is the stable identifier for this detection strategy.
const Strategy domain.Strategy = "helm"

// Detector implements port.Detector for Helm values files.
type Detector struct{}

// init makes the detector available to every binary that links this package.
func init() { registry.MustRegister(&Detector{}) }

// Name implements port.Detector.
func (d *Detector) Name() string { return string(Strategy) }

// Tier implements port.Detector.
func (d *Detector) Tier() port.Tier { return port.TierSpecialist }

// Match implements port.Detector.
// Returns true for values.yaml and its per-environment variants
// (values-prod.yaml, values.prod.yaml, prod-values.yaml, prod.values.yaml),
// with either YAML extension, case-insensitively and at any depth in the tree,
// except under .github/workflows/ and .github/actions/: GitHub treats every
// YAML file there as a workflow or action regardless of its name.
func (d *Detector) Match(filePath string) bool {
	if githubpath.IsReserved(filePath) {
		return false
	}

	lower := strings.ToLower(path.Base(filePath))
	stem, ok := strings.CutSuffix(lower, ".yaml")
	if !ok {
		if stem, ok = strings.CutSuffix(lower, ".yml"); !ok {
			return false
		}
	}

	return stem == "values" ||
		strings.HasPrefix(stem, "values-") || strings.HasPrefix(stem, "values.") ||
		strings.HasSuffix(stem, "-values") || strings.HasSuffix(stem, ".values")
}

// Detect implements port.Detector.
//
// It reports the images a values file names under an image-shaped key (e.g.
// "image", "mainImage", "init-image"), in either of two forms:
//
//   - an image block: a mapping with a "repository" plus a pinning qualifier
//     (tag or digest), optionally with a "registry". The Bitnami
//     `image: { registry?, repository, tag, digest? }` convention is the
//     canonical shape;
//   - a single string holding a pinned reference (`image: nginx:1.25`).
//
// Short forms are reported as written. Other conventions that use a
// `repository` key for non-image data (OCM repository mappings, rescoring
// rulesets that point at Git URLs, etc.) are intentionally ignored.
//
// Every other string of the file, keys included, is scanned with the strict
// imageref.Candidates filter, so fully-qualified references outside
// image-shaped keys are still found.
func (d *Detector) Detect(filePath string, content []byte) ([]domain.Finding, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("helm: parse %q: %w", filePath, err)
	}
	w := walker{filePath: filePath}
	w.walk(&root, "")
	return w.findings, nil
}

// walker collects the findings of one values file.
type walker struct {
	filePath string
	findings []domain.Finding
}

// walk visits node and everything below it.
//
// parentKey is the mapping key under which node sits, or "" at the document
// root. Sequence elements inherit their containing sequence's parent key so
// that `images: [{repository, tag}, …]` patterns are also recognised.
//
// Limitation: when a mapping node is identified as an image block, the node is
// consumed and its children are not visited. A mapping that has both a
// "repository" key and nested image sub-maps will therefore only produce one
// finding for the outermost block.
func (w *walker) walk(node *yaml.Node, parentKey string) {
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			w.walk(child, "")
		}

	case yaml.SequenceNode:
		for _, child := range node.Content {
			w.walk(child, parentKey)
		}

	case yaml.MappingNode:
		// Only attempt image-block extraction when the parent key looks like
		// an image attribute. Without this gate, any mapping that happens to
		// carry a `repository` key (OCM repo mappings, rescoring-config
		// references, registry endpoint blocks, …) is mis-reported.
		if isImageParent(parentKey) {
			if raw, line, ok := imageBlock(node); ok {
				w.report(imageref.Parse(raw), line)
				return
			}
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			// Keys are text too: a reference used as a mapping key is reported.
			yamlscan.Scalar(node.Content[i], w.report)
			w.walk(node.Content[i+1], node.Content[i].Value)
		}

	case yaml.ScalarNode:
		if isImageParent(parentKey) && isPinnedImage(node.Value) {
			if !suppress.IsIgnoredLine(node.LineComment) {
				w.report(imageref.Parse(node.Value), node.Line)
			}
			return
		}
		yamlscan.Scalar(node, w.report)
	}
}

func (w *walker) report(ref domain.ImageRef, line int) {
	w.findings = append(w.findings, domain.Finding{
		Ref:      ref,
		FilePath: w.filePath,
		Line:     uint(line),
		Strategy: Strategy,
	})
}

// imageParentSuffixes are the case-insensitive suffixes that mark a mapping
// key as an image attribute. Both singular and plural forms are accepted —
// the plural covers the common `images: [{repository, tag}, …]` collection
// pattern whose elements inherit the parent key.
//
// Matching is ends-with, which lets the exact key, camel-cased keys
// (`mainImage`, `extraImages`) and kebab- or snake-cased ones (`init-image`,
// `metrics_image`) through while still rejecting unrelated keys.
var imageParentSuffixes = []string{"image", "images"}

// isImageParent reports whether key looks like a Helm image attribute name.
func isImageParent(key string) bool {
	lower := strings.ToLower(key)
	for _, suffix := range imageParentSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// imageBlockKeys are the keys an image block is assembled from.
var imageBlockKeys = []string{"registry", "repository", "tag", "digest"}

// blockIgnored reports whether any line of an image block carries the inline
// suppression annotation in its YAML line comment.
func blockIgnored(node *yaml.Node) bool {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if slices.Contains(imageBlockKeys, node.Content[i].Value) &&
			suppress.IsIgnoredLine(node.Content[i+1].LineComment) {
			return true
		}
	}
	return false
}

// imageBlock assembles the reference an image block names and returns it with
// the line of its repository. ok=false means node is not an image block, or is
// suppressed; callers fall back to visiting its children.
//
// A mapping qualifies as an image block only if it carries a "repository" key
// AND a pinning qualifier — a non-empty "tag" or "digest". Tag-less references
// are intentionally rejected: the consumers of this tool need scannable
// (i.e. version-pinned) image references, and a registry path on its own is
// almost always non-image config (an OCM repository, a registry endpoint, …).
func imageBlock(node *yaml.Node) (raw string, line int, ok bool) {
	repo, line := mapValue(node, "repository")
	host, _ := mapValue(node, "registry")
	tag, _ := mapValue(node, "tag")
	digest, _ := mapValue(node, "digest")
	if repo == "" || (tag == "" && digest == "") || blockIgnored(node) {
		return "", 0, false
	}

	raw = buildRaw(host, repo, tag, digest)
	return raw, line, isReference(raw)
}

// isPinnedImage reports whether value, a string under an image-shaped key, is
// a reference with a tag or digest. A tag-less value is rejected for the same
// reason a tag-less image block is: on its own it is as likely to be a file
// name or a flag (`backgroundImage: logo.png`) as an image.
func isPinnedImage(value string) bool {
	pinned := strings.Contains(value, "@") ||
		strings.LastIndex(value, ":") > strings.LastIndex(value, "/")
	return pinned && isReference(value)
}

// isReference reports whether raw can be reported as an image reference.
//
// A value containing `://` is a URL literal (a Git repository, a Helm `oci://`
// chart reference, an https endpoint). go-containerregistry would otherwise
// accept strings like `https://example.com/org/app:v1`, because the trailing
// `:v1` matches its tag grammar.
//
// A value that does not parse is kept only when it contains a template
// placeholder (`{{ .Values.imageTag }}`, `${var}`): the version is unresolved
// at scan time, which is a legitimate finding. Any other parse failure is not
// a reference.
func isReference(raw string) bool {
	if strings.Contains(raw, "://") {
		return false
	}
	return imageref.Parse(raw).Parsed || imageref.ContainsTemplate(raw)
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
func buildRaw(host, repo, tag, digest string) string {
	var sb strings.Builder
	if host != "" {
		sb.WriteString(host)
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
