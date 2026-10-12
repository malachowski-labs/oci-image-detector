// Package githubactions provides a Detector for GitHub Actions workflow files.
package githubactions

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/malachowski-labs/oci-image-detector/internal/githubpath"
	"github.com/malachowski-labs/oci-image-detector/pkg/domain"
	"github.com/malachowski-labs/oci-image-detector/pkg/imageref"
	"github.com/malachowski-labs/oci-image-detector/pkg/port"
	"github.com/malachowski-labs/oci-image-detector/pkg/registry"
	"github.com/malachowski-labs/oci-image-detector/pkg/suppress"
)

// Strategy is the stable identifier for this detection strategy.
const Strategy domain.Strategy = "githubactions"

// Detector implements port.Detector for GitHub Actions workflow and action files.
type Detector struct{}

// New returns a new GitHub Actions Detector.
func New() *Detector { return &Detector{} }

// init makes the detector available to every binary that links this package.
func init() { registry.MustRegister(New()) }

// Name implements port.Detector.
func (d *Detector) Name() string { return string(Strategy) }

// Tier implements port.Detector.
func (d *Detector) Tier() port.Tier { return port.TierSpecialist }

// Match implements port.Detector.
// Returns true for .yml/.yaml files under .github/workflows/ or .github/actions/.
func (d *Detector) Match(filePath string) bool {
	lower := strings.ToLower(filePath)
	if !strings.HasSuffix(lower, ".yml") && !strings.HasSuffix(lower, ".yaml") {
		return false
	}
	return githubpath.IsReserved(filePath)
}

// Detect implements port.Detector.
// It reports OCI image references from three locations in a workflow file:
//
//   - Step uses fields with the docker:// prefix (e.g. uses: docker://alpine:3.12)
//   - Job-level container images (container.image or the shorthand container: image)
//   - Service container images (services.<name>.image)
func (d *Detector) Detect(filePath string, content []byte) ([]domain.Finding, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("githubactions: parse %q: %w", filePath, err)
	}
	var findings []domain.Finding
	walkNode(&root, "", filePath, &findings)
	return findings, nil
}

// serviceEntry is the parentKey handed to the entries of a "services" mapping.
// Their own keys are user-chosen service names, so the walk replaces them with
// this marker to recognise the "image" key of each service block.
const serviceEntry = "service"

// walkNode recursively walks a yaml.Node collecting OCI image findings.
// parentKey is the mapping key under which this node sits, or "" at the
// document root.
func walkNode(node *yaml.Node, parentKey, filePath string, findings *[]domain.Finding) {
	switch node.Kind {
	case yaml.DocumentNode:
		for _, c := range node.Content {
			walkNode(c, "", filePath, findings)
		}

	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, val := node.Content[i].Value, node.Content[i+1]

			if val.Kind != yaml.ScalarNode {
				childKey := key
				if parentKey == "services" {
					childKey = serviceEntry
				}
				walkNode(val, childKey, filePath, findings)
				continue
			}

			// An empty or null value ("container:", "image: ~") names no image.
			if val.Tag == "!!null" || val.Value == "" {
				continue
			}
			raw, ok := imageValue(key, parentKey, val.Value)
			if !ok || suppress.IsIgnoredLine(val.LineComment) {
				continue
			}
			*findings = append(*findings, domain.Finding{
				Ref:      imageref.Parse(raw),
				FilePath: filePath,
				Line:     uint(val.Line),
				Strategy: Strategy,
			})
		}

	case yaml.SequenceNode:
		for _, c := range node.Content {
			walkNode(c, parentKey, filePath, findings)
		}
	}
}

// imageValue returns the image reference a scalar mapping entry names, if any.
// key is the entry's own key and parentKey the key of the mapping holding it.
func imageValue(key, parentKey, value string) (string, bool) {
	switch {
	case key == "uses":
		// Step: uses: docker://image:tag
		return stripDockerScheme(value)
	case key == "container":
		// Job-level container shorthand: container: image:tag
		return value, true
	case key == "image" && (parentKey == "container" || parentKey == serviceEntry):
		// image: within a container or service block
		return value, true
	}
	return "", false
}

// stripDockerScheme removes the "docker://" prefix from a step "uses" value.
// Returns ("", false) for any value that does not start with "docker://".
func stripDockerScheme(uses string) (string, bool) {
	raw, ok := strings.CutPrefix(uses, "docker://")
	if !ok || raw == "" {
		return "", false
	}
	return raw, true
}
