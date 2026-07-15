package cmd

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/spf13/cobra"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/terraform"
	"github.com/malachowski-labs/oci-image-detector/internal/domain"
)

// planOptions holds the parsed flags for the plan subcommand.
type planOptions struct {
	outputFile    string
	excludeImages []string
	verbose       bool
	allowEmpty    bool
}

// newPlanCmd builds the `plan` subcommand, which extracts image references from
// the JSON produced by `terraform show -json <tfplan>`. Unlike the root scan,
// the plan already has every variable, local, function and module input
// resolved by Terraform, so it is the complete counterpart to the best-effort
// static resolution of the directory scan.
func newPlanCmd() *cobra.Command {
	var opts planOptions

	cmd := &cobra.Command{
		Use:   "plan [flags] <plan.json>",
		Short: "Scan a Terraform plan (terraform show -json) for image references",
		Long: `plan reads the JSON output of 'terraform show -json <tfplan>' and reports
the OCI image references found in the fully-resolved planned values.

Pass '-' as the file to read the plan JSON from stdin, e.g.:
  terraform show -json tfplan | oci-image-detector plan -

Findings are attributed to the resource address (e.g.
module.app.aws_ecs_task_definition.app).`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return runPlan(args[0], opts)
		},
	}

	f := cmd.Flags()
	f.StringVarP(&opts.outputFile, "output-file", "o", "",
		"Write findings as JSON to this file (in addition to stdout)")
	f.StringArrayVar(&opts.excludeImages, "exclude-images", nil,
		"Glob pattern to exclude by image reference (repeatable, doublestar syntax, e.g. 'localhost:5000/**')")
	f.BoolVarP(&opts.verbose, "verbose", "v", false, "Enable debug logging on stderr")
	f.BoolVar(&opts.allowEmpty, "allow-empty", false,
		"Exit 0 when no image references are found (default: exit 1)")

	return cmd
}

// runPlan reads the plan JSON (from a file or stdin), extracts findings and
// reports them through the shared reporter helper.
func runPlan(path string, opts planOptions) error {
	log := buildLogger(opts.verbose)
	defer log.Sync() //nolint:errcheck

	content, err := readPlanInput(path)
	if err != nil {
		return fmt.Errorf("read plan: %w", err)
	}

	findings, err := terraform.DetectPlan(content)
	if err != nil {
		return err
	}

	findings, err = filterExcludedImages(findings, opts.excludeImages)
	if err != nil {
		return err
	}
	sortFindings(findings)

	return report(findings, opts.outputFile, opts.allowEmpty, log)
}

// filterExcludedImages removes findings whose raw image reference matches any of
// the doublestar glob patterns, mirroring the ScanService filter so the plan
// subcommand honors --exclude-images identically to the directory scan.
func filterExcludedImages(findings []domain.Finding, patterns []string) ([]domain.Finding, error) {
	if len(patterns) == 0 {
		return findings, nil
	}
	out := findings[:0]
	for _, f := range findings {
		matched := false
		for _, pattern := range patterns {
			ok, err := doublestar.Match(pattern, f.Ref.Raw)
			if err != nil {
				return nil, fmt.Errorf("invalid image exclude pattern %q: %w", pattern, err)
			}
			if ok {
				matched = true
				break
			}
		}
		if !matched {
			out = append(out, f)
		}
	}
	return out, nil
}

// readPlanInput reads the plan JSON from path, or from stdin when path is "-".
func readPlanInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// sortFindings orders findings deterministically by FilePath → Line → Raw,
// matching the ordering the ScanService applies to directory-scan results.
func sortFindings(findings []domain.Finding) {
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.FilePath != b.FilePath {
			return a.FilePath < b.FilePath
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Ref.Raw < b.Ref.Raw
	})
}
