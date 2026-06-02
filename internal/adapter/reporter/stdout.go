// Package reporter provides implementations of port.Reporter.
package reporter

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// Stdout writes a human-readable findings report to an io.Writer (typically
// os.Stdout). It is always registered; the JSON reporter is added on top when
// --output-file is set.
type Stdout struct {
	w io.Writer
}

// NewStdout returns a Stdout reporter that writes to w.
func NewStdout(w io.Writer) *Stdout {
	return &Stdout{w: w}
}

// Report implements port.Reporter.
// Output format (tab-aligned):
//
//	<file>:<line>   <raw>   [<strategy>]
//	<file>:<line>   <raw>   -> <canonical>   [<strategy>]      ← when canonical ≠ raw
//	<file>:<line>   <raw>   [<strategy>]   (unresolved)        ← when not parsed
func (r *Stdout) Report(findings []domain.Finding) error {
	if len(findings) == 0 {
		fmt.Fprintln(r.w, "No image references found.")
		return nil
	}

	fmt.Fprintf(r.w, "Found %d image reference(s):\n\n", len(findings))

	// Use tabwriter so columns align regardless of path or ref length.
	tw := tabwriter.NewWriter(r.w, 0, 0, 3, ' ', 0)
	for _, f := range findings {
		location := fmt.Sprintf("%s:%d", f.FilePath, f.Line)
		strategy := fmt.Sprintf("[%s]", f.Strategy)

		if !f.Ref.Parsed {
			fmt.Fprintf(tw, "%s\t%s\t%s\t(unresolved)\n",
				location, f.Ref.Raw, strategy)
			continue
		}

		canonical := f.Ref.Canonical()
		if canonical != f.Ref.Raw {
			fmt.Fprintf(tw, "%s\t%s\t-> %s\t%s\n",
				location, f.Ref.Raw, canonical, strategy)
		} else {
			fmt.Fprintf(tw, "%s\t%s\t%s\n",
				location, f.Ref.Raw, strategy)
		}
	}

	return tw.Flush()
}
