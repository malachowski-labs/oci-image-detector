// Package yamlscan finds image references in the free text of a YAML
// document: the strings a detector has no structural reason to treat as an
// image.
//
// The YAML detectors share it so that claiming a file from the fallback
// detector loses none of the fully-qualified references that detector would
// have reported.
package yamlscan

import (
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/malachowski-labs/oci-image-detector/pkg/domain"
	"github.com/malachowski-labs/oci-image-detector/pkg/imageref"
	"github.com/malachowski-labs/oci-image-detector/pkg/suppress"
)

// HasValue reports whether node is a non-empty scalar. An explicit null
// ("image: ~") is a scalar too, but names nothing.
func HasValue(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.Tag != "!!null" && node.Value != ""
}

// Scalar calls report for every fully-qualified image reference inside a
// scalar node, as selected by the strict imageref.Candidates filter, with the
// 1-based line the reference sits on. Nodes of any other kind hold no text and
// yield nothing.
//
// The inline ignore annotation is honoured as a YAML comment on the value and,
// inside a multi-line value, on the reference's own line.
func Scalar(node *yaml.Node, report func(ref domain.ImageRef, line int)) {
	if node.Kind != yaml.ScalarNode || suppress.IsIgnoredLine(node.LineComment) {
		return
	}
	// A literal block keeps its line breaks and starts on the line after its
	// indicator, so a reference can be attributed to its own line. Any other
	// style may fold lines, and is attributed to the line the value starts on.
	literal := node.Style&yaml.LiteralStyle != 0
	for i, line := range strings.Split(node.Value, "\n") {
		if suppress.IsIgnoredLine(line) {
			continue
		}
		at := node.Line
		if literal {
			at += i + 1
		}
		for _, raw := range imageref.Candidates(line) {
			report(imageref.Parse(raw), at)
		}
	}
}
