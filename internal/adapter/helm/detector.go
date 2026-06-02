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
// Walks the YAML document tree looking for mapping nodes that have a
// "repository" key. When found it combines registry (if present), repository,
// and tag (if present) into a single image reference.
func (d *Detector) Detect(filePath string, content []byte) ([]domain.Finding, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("helm: parse %q: %w", filePath, err)
	}
	if root.Kind == 0 {
		return nil, nil // empty file
	}

	var findings []domain.Finding
	walkNode(&root, filePath, &findings)
	return findings, nil
}

// walkNode recursively traverses a yaml.Node looking for image map nodes.
//
// Limitation: when a mapping node is identified as an image block (it contains
// a "repository" key), the node is consumed and its children are not recursed
// into. A mapping that has both a "repository" key and nested image sub-maps
// will therefore only produce one finding for the outermost block. This is an
// accepted v1 limitation; such deeply nested patterns are not a common Helm
// convention.
func walkNode(node *yaml.Node, filePath string, findings *[]domain.Finding) {
	if node.Kind == yaml.DocumentNode {
		for _, child := range node.Content {
			walkNode(child, filePath, findings)
		}
		return
	}

	if node.Kind == yaml.MappingNode {
		repo, repoLine := mapValue(node, "repository")
		if repo != "" {
			registry, _ := mapValue(node, "registry")
			tag, _ := mapValue(node, "tag")
			raw := buildRaw(registry, repo, tag)
			ref := imageref.Parse(raw)
			*findings = append(*findings, domain.Finding{
				Ref:      ref,
				FilePath: filePath,
				Line:     uint(repoLine),
				Strategy: Strategy,
			})
			// Don't recurse into this node — we already consumed it.
			// See the Limitation note on walkNode.
			return
		}
		// Recurse into values of this map.
		for i := 1; i < len(node.Content); i += 2 {
			walkNode(node.Content[i], filePath, findings)
		}
		return
	}

	if node.Kind == yaml.SequenceNode {
		for _, child := range node.Content {
			walkNode(child, filePath, findings)
		}
	}
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
// into a raw image reference string.
func buildRaw(registry, repo, tag string) string {
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
	return sb.String()
}
