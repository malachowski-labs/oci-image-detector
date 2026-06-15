// Package githubactions provides a Detector for GitHub Actions workflow files.
package githubactions

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/imageref"
	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// Strategy is the stable identifier for this detection strategy.
const Strategy domain.Strategy = "githubactions"

// Detector implements port.Detector for GitHub Actions workflow and action files.
type Detector struct{}

// New returns a new GitHub Actions Detector.
func New() *Detector { return &Detector{} }

// Name implements port.Detector.
func (d *Detector) Name() string { return string(Strategy) }

// Match implements port.Detector.
// Returns true for .yml/.yaml files under .github/workflows/ or .github/actions/.
func (d *Detector) Match(filePath string) bool {
	lower := strings.ToLower(filePath)
	if !strings.HasSuffix(lower, ".yml") && !strings.HasSuffix(lower, ".yaml") {
		return false
	}
	return strings.Contains(lower, ".github/workflows/") ||
		strings.Contains(lower, ".github/actions/")
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
	if root.Kind == 0 {
		return nil, nil
	}
	var findings []domain.Finding
	walkNode(&root, "", filePath, &findings)
	return findings, nil
}

// walkNode recursively walks a yaml.Node collecting OCI image findings.
// parentKey is the mapping key under which this node sits, or "" at the
// document root. Nodes under a "services" mapping pass "service" as parentKey
// so that each named service block's "image" key is recognised.
func walkNode(node *yaml.Node, parentKey, filePath string, findings *[]domain.Finding) {
	switch node.Kind {
	case yaml.DocumentNode:
		for _, c := range node.Content {
			walkNode(c, "", filePath, findings)
		}

	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			val := node.Content[i+1]

			// Step: uses: docker://image:tag
			if key == "uses" && val.Kind == yaml.ScalarNode {
				if !imageref.IsIgnoredLine(val.LineComment) {
					if raw, ok := stripDockerScheme(val.Value); ok {
						*findings = append(*findings, newFinding(raw, uint(val.Line), filePath))
					}
				}
				continue
			}

			// Job-level container shorthand: container: image:tag (scalar form)
			if key == "container" && val.Kind == yaml.ScalarNode {
				if !imageref.IsIgnoredLine(val.LineComment) {
					*findings = append(*findings, newFinding(val.Value, uint(val.Line), filePath))
				}
				continue
			}

			// image: within a container or service block
			if key == "image" && val.Kind == yaml.ScalarNode &&
				(parentKey == "container" || parentKey == "service") {
				if !imageref.IsIgnoredLine(val.LineComment) {
					*findings = append(*findings, newFinding(val.Value, uint(val.Line), filePath))
				}
				continue
			}

			// Recurse. Service entries (keys directly under a "services" mapping)
			// pass "service" as parentKey so their image: child is recognised.
			childCtx := key
			if parentKey == "services" {
				childCtx = "service"
			}
			walkNode(val, childCtx, filePath, findings)
		}

	case yaml.SequenceNode:
		for _, c := range node.Content {
			walkNode(c, parentKey, filePath, findings)
		}
	}
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

func newFinding(raw string, line uint, filePath string) domain.Finding {
	return domain.Finding{
		Ref:      imageref.Parse(raw),
		FilePath: filePath,
		Line:     line,
		Strategy: Strategy,
	}
}
