// Package cmd wires together the CLI flags, detectors, scan service, and
// reporters into the runnable oci-image-detector command.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/dockerfile"
	"github.com/malachowski-labs/oci-image-detector/internal/adapter/generic"
	"github.com/malachowski-labs/oci-image-detector/internal/adapter/githubactions"
	"github.com/malachowski-labs/oci-image-detector/internal/adapter/helm"
	"github.com/malachowski-labs/oci-image-detector/internal/adapter/reporter"
	"github.com/malachowski-labs/oci-image-detector/internal/adapter/terraform"
	"github.com/malachowski-labs/oci-image-detector/internal/domain"
	"github.com/malachowski-labs/oci-image-detector/internal/port"
	"github.com/malachowski-labs/oci-image-detector/internal/service"
)

// builtinExcludes are patterns always applied before user-supplied --exclude
// values. Users cannot override these; they represent paths that should never
// be scanned regardless of the invocation context.
// builtinExcludes are patterns always prepended to user-supplied --exclude
// values. These paths should never be scanned regardless of the invocation
// context and cannot be overridden by the caller.
var builtinExcludes = []string{
	".git/**", // git internals — never contain image refs, produce many false positives
	"go.sum",  // Go module hash file — content hashes, never image refs
}

// ErrNoFindings is returned by the root command when the scan produces zero
// findings and --allow-empty was not set. The caller (Execute) prints it and
// exits with code 1.
var ErrNoFindings = errors.New("no image references found (use --allow-empty to suppress this error)")

// options holds the parsed CLI flags for a single invocation.
type options struct {
	directory     string
	exclude       []string
	excludeImages []string
	outputFile    string
	verbose       bool
	allowEmpty    bool
}

// Execute builds the root cobra command, executes it, and exits on error.
// version is injected by main from the -ldflags build variable.
// It is the sole entry point called from main.
func Execute(version string) {
	if err := newRootCmd(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// newRootCmd constructs the cobra.Command with all flags bound to an options
// struct. Separating construction from execution makes the command testable
// without spawning a subprocess.
func newRootCmd(version string) *cobra.Command {
	var opts options

	cmd := &cobra.Command{
		Use:     "oci-image-detector",
		Version: version,
		Short:   "Scan a directory tree for OCI/Docker image references",
		Long: `oci-image-detector recursively scans a directory for OCI image references
in Dockerfiles, Helm values files, Terraform configs, and arbitrary text files.

Results are always written to stdout in human-readable format.
When --output-file is set a machine-readable JSON report is also written.

Exit codes:
  0  scan completed; findings (or --allow-empty set)
  1  scan error, or no findings found and --allow-empty not set`,

		// Suppress the default "Usage:" dump on RunE errors and cobra's own
		// error printing so we control the format ourselves in Execute.
		SilenceUsage:  true,
		SilenceErrors: true,

		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), opts)
		},
	}

	f := cmd.Flags()
	f.StringVarP(&opts.directory, "directory", "d", ".", "Root directory to scan")
	f.StringArrayVarP(&opts.exclude, "exclude", "e", nil,
		"Glob pattern to exclude (repeatable, doublestar syntax, e.g. 'vendor/**')")
	f.StringArrayVar(&opts.excludeImages, "exclude-images", nil,
		"Glob pattern to exclude by image reference (repeatable, doublestar syntax, e.g. 'localhost:5000/**')")
	f.StringVarP(&opts.outputFile, "output-file", "o", "",
		"Write findings as JSON to this file (in addition to stdout)")
	f.BoolVarP(&opts.verbose, "verbose", "v", false, "Enable debug logging on stderr")
	f.BoolVar(&opts.allowEmpty, "allow-empty", false,
		"Exit 0 when no image references are found (default: exit 1)")

	cmd.AddCommand(newPlanCmd())

	return cmd
}

// run is the core of the command. It builds the logger, wires detectors into
// a ScanService, executes the scan, and passes results to all reporters.
func run(ctx context.Context, opts options) error {
	log := buildLogger(opts.verbose)
	defer log.Sync() //nolint:errcheck

	// Respect SIGINT / SIGTERM: cancel the scan context so WalkDir unwinds.
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	scanner := service.NewScanService(
		[]port.Detector{
			// Order matters: first match wins. Specialist detectors before generic.
			dockerfile.New(),
			helm.New(),
			githubactions.New(),
			generic.New(),
		},
		[]port.DirectoryAwareDetector{
			terraform.New(),
		},
		log,
	)

	findings, err := scanner.Scan(ctx, opts.directory, port.ScanOptions{
		Exclude:       append(builtinExcludes, opts.exclude...),
		ExcludeImages: opts.excludeImages,
	})
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	return report(findings, opts.outputFile, opts.allowEmpty, log)
}

// report writes findings to stdout and, when outputFile is set, also as JSON.
// It returns ErrNoFindings when there are no findings and allowEmpty is false.
// Shared by the root scan command and the plan subcommand so both surface
// results identically.
func report(findings []domain.Finding, outputFile string, allowEmpty bool, log *zap.Logger) error {
	// Always report to stdout; optionally also write JSON.
	reporters := []port.Reporter{reporter.NewStdout(os.Stdout)}
	if outputFile != "" {
		reporters = append(reporters, reporter.NewJSONFile(outputFile))
	}

	for _, r := range reporters {
		if err := r.Report(findings); err != nil {
			log.Error("reporter error", zap.Error(err))
		}
	}

	if len(findings) == 0 && !allowEmpty {
		return ErrNoFindings
	}

	return nil
}

// buildLogger constructs a zap.Logger that always writes to stderr so that
// stdout stays clean for findings output.
func buildLogger(verbose bool) *zap.Logger {
	var cfg zap.Config
	if verbose {
		cfg = zap.NewDevelopmentConfig()
	} else {
		cfg = zap.NewProductionConfig()
	}
	// Redirect all log output to stderr.
	cfg.OutputPaths = []string{"stderr"}
	cfg.ErrorOutputPaths = []string{"stderr"}

	log, err := cfg.Build()
	if err != nil {
		// Fallback to a no-op logger rather than crashing on logger init.
		return zap.NewNop()
	}
	return log
}
