// Package compose provides a Detector for Docker Compose files.
package compose

import (
	"fmt"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/malachowski-labs/oci-image-detector/pkg/domain"
	"github.com/malachowski-labs/oci-image-detector/pkg/imageref"
	"github.com/malachowski-labs/oci-image-detector/pkg/port"
	"github.com/malachowski-labs/oci-image-detector/pkg/registry"
	"github.com/malachowski-labs/oci-image-detector/pkg/suppress"
)

// Strategy is the stable identifier for this detection strategy.
const Strategy domain.Strategy = "compose"

// mergeKey is the YAML merge key, which pulls the keys of an aliased mapping
// (or a sequence of them) into the mapping that carries it.
const mergeKey = "<<"

// Detector implements port.Detector for Docker Compose files.
type Detector struct{}

// New returns a new Docker Compose Detector.
func New() *Detector { return &Detector{} }

// init makes the detector available to every binary that links this package.
func init() { registry.MustRegister(New()) }

// Name implements port.Detector.
func (d *Detector) Name() string { return string(Strategy) }

// Tier implements port.Detector.
func (d *Detector) Tier() port.Tier { return port.TierSpecialist }

// Match implements port.Detector.
// Returns true for compose.yaml and docker-compose.yaml, their .yml spellings
// and their override variants (compose.<suffix>.yaml, e.g. compose.prod.yaml),
// case-insensitively and at any depth in the tree, except under
// .github/workflows/ and .github/actions/: GitHub treats every YAML file there
// as a workflow or action regardless of its name.
func (d *Detector) Match(filePath string) bool {
	lowerPath := strings.ToLower(filePath)
	if strings.Contains(lowerPath, ".github/workflows/") ||
		strings.Contains(lowerPath, ".github/actions/") {
		return false
	}

	lower := path.Base(lowerPath)
	stem, ok := strings.CutSuffix(lower, ".yaml")
	if !ok {
		if stem, ok = strings.CutSuffix(lower, ".yml"); !ok {
			return false
		}
	}

	return stem == "compose" || strings.HasPrefix(stem, "compose.") ||
		stem == "docker-compose" || strings.HasPrefix(stem, "docker-compose.")
}

// Detect implements port.Detector.
//
// It reports the image of every service, i.e. the services.<name>.image
// scalars, and nothing else: an "image" key anywhere else in the file is not a
// service image. The field is unambiguous, so short forms ("nginx:1.25") and
// tag-less references ("nginx") are reported as written.
//
// Compose interpolation ("${TAG}", "${TAG:-1.25}") is not resolved; such
// references surface as raw, unparsed findings.
//
// Aliases and merge keys are followed, so a service that takes its image from
// a shared anchor (typically an "x-" extension field) is covered. Such an image
// is reported once, at the line where it is written.
func (d *Detector) Detect(filePath string, content []byte) ([]domain.Finding, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("compose: parse %q: %w", filePath, err)
	}
	if root.Kind == 0 {
		return nil, nil // empty file
	}

	services := mapValue(resolve(root.Content[0]), "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil, nil
	}

	var findings []domain.Finding
	seen := map[*yaml.Node]bool{} // image nodes shared through an anchor

	for i := 0; i+1 < len(services.Content); i += 2 {
		image := mapValue(resolve(services.Content[i+1]), "image")
		if image == nil || !isImageValue(image) || seen[image] {
			continue
		}
		seen[image] = true

		if suppress.IsIgnoredLine(image.LineComment) {
			continue
		}
		findings = append(findings, domain.Finding{
			Ref:      imageref.Parse(image.Value),
			FilePath: filePath,
			Line:     uint(image.Line),
			Strategy: Strategy,
		})
	}

	return findings, nil
}

// isImageValue reports whether node is a non-empty scalar. An explicit null
// ("image: ~", "image: null") is a scalar too, but names no image.
func isImageValue(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.Tag != "!!null" && node.Value != ""
}

// resolve returns the node an alias points at, or node itself when it is not
// an alias.
func resolve(node *yaml.Node) *yaml.Node {
	if node.Kind == yaml.AliasNode {
		return node.Alias
	}
	return node
}

// mapValue returns the resolved value for key in a MappingNode, or nil when
// node is not a mapping or does not carry the key. A key written in the mapping
// wins over one pulled in through a merge key, and among merged mappings the
// first one wins, as the YAML merge key specifies.
func mapValue(node *yaml.Node, key string) *yaml.Node {
	return lookup(node, key, map[*yaml.Node]bool{})
}

// lookup implements mapValue. visited holds the mappings already searched, so
// a mapping that merges itself, directly or through a nested anchor, ends the
// search instead of recursing without bound.
func lookup(node *yaml.Node, key string, visited map[*yaml.Node]bool) *yaml.Node {
	if node.Kind != yaml.MappingNode || visited[node] {
		return nil
	}
	visited[node] = true

	var merged *yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		switch node.Content[i].Value {
		case key:
			return resolve(node.Content[i+1])
		case mergeKey:
			merged = node.Content[i+1]
		}
	}
	if merged == nil {
		return nil
	}

	if merged.Kind == yaml.SequenceNode {
		for _, m := range merged.Content {
			if v := lookup(resolve(m), key, visited); v != nil {
				return v
			}
		}
		return nil
	}
	return lookup(resolve(merged), key, visited)
}
