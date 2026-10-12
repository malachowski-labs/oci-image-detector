package service

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"

	"github.com/bmatcuk/doublestar/v4"
	"go.uber.org/zap"

	"github.com/malachowski-labs/oci-image-detector/pkg/domain"
	"github.com/malachowski-labs/oci-image-detector/pkg/port"
)

// ScanService runs a scan. It walks a directory tree, dispatches
// files and directories to the registered detectors, and returns a sorted
// slice of findings.
type ScanService struct {
	detectors    []port.Detector
	dirDetectors []port.DirectoryAwareDetector
	log          *zap.Logger
}

// NewScanService constructs a ScanService.
// detectors handles individual files; dirDetectors handles whole directories
// (e.g. Terraform, which needs cross-file variable resolution).
// detectors are consulted in the order given, which pkg/registry produces
// from each detector's declared tier; see claimant for which match wins.
// Every dirDetector that matches a directory runs on it.
func NewScanService(
	detectors []port.Detector,
	dirDetectors []port.DirectoryAwareDetector,
	log *zap.Logger,
) *ScanService {
	return &ScanService{
		detectors:    detectors,
		dirDetectors: dirDetectors,
		log:          log,
	}
}

// Scan opens dir from the real filesystem and
// delegates to ScanFS.
func (s *ScanService) Scan(ctx context.Context, dir string, opts port.ScanOptions) ([]domain.Finding, error) {
	return s.ScanFS(ctx, os.DirFS(dir), opts)
}

// ScanFS runs the scan against the provided fs.FS. It is the primary
// implementation and is separated from Scan so tests can inject an in-memory
// filesystem without touching the real disk.
func (s *ScanService) ScanFS(ctx context.Context, fsys fs.FS, opts port.ScanOptions) ([]domain.Finding, error) {
	if err := validatePatterns(opts.Exclude); err != nil {
		return nil, err
	}
	if err := validatePatterns(opts.ExcludeImages); err != nil {
		return nil, fmt.Errorf("exclude-images: %w", err)
	}

	var findings []domain.Finding

	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			s.log.Warn("walk error", zap.String("path", p), zap.Error(err))
			return nil
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		// The scan root is never excluded: patterns are relative to it.
		if p != "." && matchesAny(opts.Exclude, p) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if !d.IsDir() {
			findings = append(findings, s.scanFile(fsys, p)...)
			return nil
		}

		findings = append(findings, s.scanDir(fsys, p)...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sortFindings(findings)

	return filterExcludedImages(findings, opts.ExcludeImages), nil
}

// filterExcludedImages removes findings whose raw image reference matches any
// of the provided doublestar glob patterns.
func filterExcludedImages(findings []domain.Finding, patterns []string) []domain.Finding {
	return slices.DeleteFunc(findings, func(f domain.Finding) bool {
		return matchesAny(patterns, f.Ref.Raw)
	})
}

// scanDir runs every DirectoryAwareDetector that matches the directory and
// returns their findings, with FilePaths relative to the scan root. The walk
// goes on into the directory afterwards: the files those detectors own are
// held back from the file-level detectors by scanFile.
func (s *ScanService) scanDir(fsys fs.FS, dirPath string) []domain.Finding {
	entries, err := fs.ReadDir(fsys, dirPath)
	if err != nil {
		s.log.Warn("cannot read directory entries", zap.String("path", dirPath), zap.Error(err))
		return nil
	}

	var findings []domain.Finding
	for _, det := range s.dirDetectors {
		if !det.MatchDir(dirPath, entries) {
			continue
		}

		subFS, err := fs.Sub(fsys, dirPath)
		if err != nil {
			s.log.Warn("cannot open sub-FS for directory",
				zap.String("path", dirPath),
				zap.String("detector", det.Name()),
				zap.Error(err))
			continue
		}

		dirFindings, err := det.DetectDir(subFS)
		if err != nil {
			s.log.Warn("detector error",
				zap.String("path", dirPath),
				zap.String("detector", det.Name()),
				zap.Error(err))
			// Use whatever partial findings the detector returned.
		}

		// Prepend dirPath so all FilePaths are relative to the scan root.
		// path.Join normalises empty or double-slash sub-paths from adapters.
		for i := range dirFindings {
			dirFindings[i].FilePath = path.Join(dirPath, dirFindings[i].FilePath)
		}
		findings = append(findings, dirFindings...)
	}
	return findings
}

// scanFile dispatches a single file to the detector that claims it and returns
// its findings. A file owned by a DirectoryAwareDetector is left to that
// detector, even when its directory did not match or could not be scanned:
// a file-level detector must not silently take over for it.
func (s *ScanService) scanFile(fsys fs.FS, filePath string) []domain.Finding {
	if s.ownedByDirDetector(filePath) {
		return nil
	}

	// The content is read only when some detector accepts the path.
	candidates := s.matchPath(filePath)
	if len(candidates) == 0 {
		return nil
	}
	content, err := fs.ReadFile(fsys, filePath)
	if err != nil {
		s.log.Warn("cannot read file", zap.String("path", filePath), zap.Error(err))
		return nil
	}

	det := claimant(candidates, filePath, content)
	if det == nil {
		return nil
	}
	findings, err := det.Detect(filePath, content)
	if err != nil {
		s.log.Warn("detector error",
			zap.String("path", filePath),
			zap.String("detector", det.Name()),
			zap.Error(err))
		// Use whatever partial findings the detector returned.
	}
	return findings
}

// ownedByDirDetector reports whether a DirectoryAwareDetector owns the file.
func (s *ScanService) ownedByDirDetector(filePath string) bool {
	return slices.ContainsFunc(s.dirDetectors, func(det port.DirectoryAwareDetector) bool {
		return det.Owns(filePath)
	})
}

// matchPath returns the detectors whose Match accepts the path, in the order
// they were registered.
func (s *ScanService) matchPath(filePath string) []port.Detector {
	var out []port.Detector
	for _, det := range s.detectors {
		if det.Match(filePath) {
			out = append(out, det)
		}
	}
	return out
}

// claimant picks the detector that handles the file among those that accepted
// its path: the first specialist that claims by path alone, else the first
// ContentMatcher that accepts the content, else the first fallback. It returns
// nil when none of them claims the file.
func claimant(candidates []port.Detector, filePath string, content []byte) port.Detector {
	var byContent []port.Detector
	var fallback port.Detector
	for _, det := range candidates {
		switch _, matchesContent := det.(port.ContentMatcher); {
		case matchesContent:
			byContent = append(byContent, det)
		case det.Tier() != port.TierFallback:
			return det
		case fallback == nil:
			fallback = det
		}
	}

	// The content is only inspected once no detector has claimed the path.
	for _, det := range byContent {
		if det.(port.ContentMatcher).MatchContent(filePath, content) {
			return det
		}
	}
	return fallback
}

// matchesAny reports whether s matches any of the provided doublestar glob
// patterns. Patterns must have been validated with validatePatterns first.
func matchesAny(patterns []string, s string) bool {
	return slices.ContainsFunc(patterns, func(pattern string) bool {
		return doublestar.MatchUnvalidated(pattern, s)
	})
}

// validatePatterns checks all patterns up-front before the walk starts so
// invalid patterns are rejected immediately rather than mid-traversal.
func validatePatterns(patterns []string) error {
	for _, p := range patterns {
		if !doublestar.ValidatePattern(p) {
			return fmt.Errorf("invalid glob pattern: %q", p)
		}
	}
	return nil
}
