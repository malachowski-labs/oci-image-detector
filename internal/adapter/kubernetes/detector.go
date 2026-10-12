// Package kubernetes provides a Detector for Kubernetes manifests.
package kubernetes

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
const Strategy domain.Strategy = "kubernetes"

// containerLists are the pod spec fields that hold containers.
var containerLists = []string{"containers", "initContainers", "ephemeralContainers"}

// Detector implements port.Detector and port.ContentMatcher for Kubernetes
// manifests.
type Detector struct{}

// init makes the detector available to every binary that links this package.
func init() { registry.MustRegister(&Detector{}) }

// Name implements port.Detector.
func (d *Detector) Name() string { return string(Strategy) }

// Tier implements port.Detector.
func (d *Detector) Tier() port.Tier { return port.TierSpecialist }

// Match implements port.Detector.
// Returns true for .yaml and .yml files, case-insensitively, except under
// .github/workflows/ and .github/actions/. Manifests follow no naming
// convention, so the path only narrows the candidates down; MatchContent
// decides.
func (d *Detector) Match(filePath string) bool {
	lower := strings.ToLower(filePath)
	if !strings.HasSuffix(lower, ".yaml") && !strings.HasSuffix(lower, ".yml") {
		return false
	}
	return !githubpath.IsReserved(filePath)
}

// MatchContent implements port.ContentMatcher.
// Returns true when the file is valid YAML and at least one of its documents
// is a Kubernetes object, i.e. carries a top-level apiVersion and kind. A file
// that does not parse, such as a Helm template with control actions on lines
// of their own, is declined.
func (d *Detector) MatchContent(_ string, content []byte) bool {
	// Cheap gate: most YAML files are not manifests and need no parsing.
	if !bytes.Contains(content, []byte("apiVersion")) || !bytes.Contains(content, []byte("kind")) {
		return false
	}
	docs, err := documents(content)
	return err == nil && slices.ContainsFunc(docs, isObject)
}

// Detect implements port.Detector.
//
// In every document that is a Kubernetes object it reports the image of each
// entry of a containers, initContainers or ephemeralContainers list, wherever
// in the object that list sits. This covers Pods, workloads and CronJobs as
// well as custom resources that embed a pod template. The field is
// unambiguous, so short forms ("nginx:1.25") and tag-less references
// ("nginx") are reported as written.
//
// Every other string of the file, keys included, is scanned with the strict
// imageref.Candidates filter, so fully-qualified references in env values,
// args, annotations or ConfigMap data are still found.
//
// A container image written as a Go template action ("{{ .Values.image }}")
// is not reported: it marks a Helm chart template, whose images are resolved
// from the chart's values files. Other placeholders ("${TAG}") are reported
// raw. Aliases are not followed: a value is reported where it is written.
func (d *Detector) Detect(filePath string, content []byte) ([]domain.Finding, error) {
	docs, err := documents(content)
	if err != nil {
		err = fmt.Errorf("kubernetes: parse %q: %w", filePath, err)
	}

	var findings []domain.Finding
	for _, doc := range docs {
		w := walker{filePath: filePath, object: isObject(doc)}
		w.walk(doc)
		findings = append(findings, w.findings...)
	}
	return findings, err
}

// documents parses every YAML document of content. On a syntax error it
// returns the documents read so far together with the error.
func documents(content []byte) ([]*yaml.Node, error) {
	var docs []*yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(content))
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return docs, nil
			}
			return docs, err
		}
		docs = append(docs, &doc)
	}
}

// isObject reports whether doc is a Kubernetes object: a mapping with a
// non-empty scalar apiVersion and kind.
func isObject(doc *yaml.Node) bool {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return false
	}
	var apiVersion, kind bool
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if val := root.Content[i+1]; yamlscan.HasValue(val) {
			switch root.Content[i].Value {
			case "apiVersion":
				apiVersion = true
			case "kind":
				kind = true
			}
		}
	}
	return apiVersion && kind
}

// walker collects the findings of one document. object records whether the
// document is a Kubernetes object, where container lists are meaningful.
type walker struct {
	filePath string
	object   bool
	findings []domain.Finding
}

// walk visits node and everything below it.
func (w *walker) walk(node *yaml.Node) {
	switch node.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range node.Content {
			w.walk(c)
		}

	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, val := node.Content[i], node.Content[i+1]
			if w.object && val.Kind == yaml.SequenceNode && slices.Contains(containerLists, key.Value) {
				for _, c := range val.Content {
					w.container(c)
				}
				continue
			}
			// Keys are text too: a reference used as a mapping key is reported.
			w.walk(key)
			w.walk(val)
		}

	case yaml.ScalarNode:
		yamlscan.Scalar(node, w.report)
	}
}

// container visits one entry of a container list: its image is reported as
// written, its other fields are walked like any other value.
func (w *walker) container(node *yaml.Node) {
	if node.Kind != yaml.MappingNode {
		w.walk(node)
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, val := node.Content[i].Value, node.Content[i+1]
		if key != "image" || val.Kind != yaml.ScalarNode {
			w.walk(val)
			continue
		}
		if !yamlscan.HasValue(val) || strings.Contains(val.Value, "{{") || suppress.IsIgnoredLine(val.LineComment) {
			continue
		}
		w.report(imageref.Parse(val.Value), val.Line)
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
