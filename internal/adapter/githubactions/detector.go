// Package githubactions provides a Detector for GitHub Actions workflow files.
package githubactions

import (
	"fmt"
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
const Strategy domain.Strategy = "githubactions"

// Detector implements port.Detector for GitHub Actions workflow and action files.
type Detector struct{}

// init makes the detector available to every binary that links this package.
func init() { registry.MustRegister(&Detector{}) }

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
// It reports OCI image references from four locations in a workflow or
// action file:
//
//   - Step uses fields with the docker:// prefix (e.g. uses: docker://alpine:3.12)
//   - Job-level container images (container.image or the shorthand container: image)
//   - Service container images (services.<name>.image)
//   - The image of a Docker container action (runs.image with the docker://
//     prefix; a path to a Dockerfile names no image)
//
// Every other string of the file, keys included, is scanned with the strict
// imageref.Candidates filter, so fully-qualified references in run scripts,
// env values or action inputs are still found.
func (d *Detector) Detect(filePath string, content []byte) ([]domain.Finding, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("githubactions: parse %q: %w", filePath, err)
	}
	w := walker{filePath: filePath}
	w.walk(&root, "")
	return w.findings, nil
}

// walker collects the findings of one workflow or action file.
type walker struct {
	filePath string
	findings []domain.Finding
}

// serviceEntry is the parentKey handed to the entries of a "services" mapping.
// Their own keys are user-chosen service names, so the walk replaces them with
// this marker to recognise the "image" key of each service block.
const serviceEntry = "service"

// walk visits node and everything below it.
// parentKey is the mapping key under which this node sits, or "" at the
// document root.
func (w *walker) walk(node *yaml.Node, parentKey string) {
	switch node.Kind {
	case yaml.DocumentNode:
		for _, c := range node.Content {
			w.walk(c, "")
		}

	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, val := node.Content[i].Value, node.Content[i+1]
			// Keys are text too: a reference used as a mapping key is reported.
			yamlscan.Scalar(node.Content[i], w.report)

			if val.Kind != yaml.ScalarNode {
				childKey := key
				if parentKey == "services" {
					childKey = serviceEntry
				}
				w.walk(val, childKey)
				continue
			}

			// An empty or null value ("container:", "image: ~") names no image.
			if val.Tag == "!!null" || val.Value == "" {
				continue
			}
			raw, ok := imageValue(key, parentKey, val.Value)
			if !ok {
				yamlscan.Scalar(val, w.report)
				continue
			}
			if !suppress.IsIgnoredLine(val.LineComment) {
				w.report(imageref.Parse(raw), val.Line)
			}
		}

	case yaml.SequenceNode:
		for _, c := range node.Content {
			w.walk(c, parentKey)
		}

	case yaml.ScalarNode:
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
	case key == "image" && parentKey == "runs":
		// Docker container action: image: docker://image:tag
		return stripDockerScheme(value)
	}
	return "", false
}

// stripDockerScheme removes the "docker://" prefix from a step "uses" or an
// action "runs.image" value.
// Returns ("", false) for any value that does not start with "docker://".
func stripDockerScheme(uses string) (string, bool) {
	raw, ok := strings.CutPrefix(uses, "docker://")
	if !ok || raw == "" {
		return "", false
	}
	return raw, true
}
