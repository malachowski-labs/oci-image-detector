package service

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"

	"github.com/bmatcuk/doublestar/v4"
	"go.uber.org/zap"

	"github.com/malachowski-labs/oci-image-detector/internal/domain"
	"github.com/malachowski-labs/oci-image-detector/internal/port"
)

// Compile-time check that *ScanService satisfies port.Scanner.
var _ port.Scanner = (*ScanService)(nil)

// ScanService implements port.Scanner. It walks a directory tree, dispatches
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

// Scan implements port.Scanner. It opens dir from the real filesystem and
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

	var findings []domain.Finding

	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			s.log.Warn("walk error", zap.String("path", p), zap.Error(err))
			return nil
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		if p == "." {
			return nil
		}

		if excluded, err := s.isExcluded(p, opts.Exclude); err != nil {
			// Already validated above; this branch is unreachable in practice.
			return fmt.Errorf("exclude pattern error for %q: %w", p, err)
		} else if excluded {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return s.handleDir(fsys, p, &findings)
		}

		return s.handleFile(fsys, p, &findings)
	})
	if err != nil {
		return nil, err
	}

	sortFindings(findings)
	return findings, nil
}

// handleDir checks whether any DirectoryAwareDetector claims this directory.
// When a detector matches it calls DetectDir, prepends dirPath to all returned
// FilePaths, and returns fs.SkipDir so WalkDir does not descend further.
// fs.SkipDir is the sole mechanism preventing double-reporting — there is no
// need for a separate "claimed dirs" bookkeeping structure.
// On fs.Sub failure the directory is skipped entirely (fail-closed) so file
// detectors do not silently take over for a directory the dir-detector claimed.
func (s *ScanService) handleDir(fsys fs.FS, dirPath string, findings *[]domain.Finding) error {
	entries, err := fs.ReadDir(fsys, dirPath)
	if err != nil {
		s.log.Warn("cannot read directory entries", zap.String("path", dirPath), zap.Error(err))
		return nil
	}

	for _, det := range s.dirDetectors {
		if !det.MatchDir(dirPath, entries) {
			continue
		}

		subFS, err := fs.Sub(fsys, dirPath)
		if err != nil {
			s.log.Warn("cannot open sub-FS for directory — skipping subtree",
				zap.String("path", dirPath),
				zap.String("detector", det.Name()),
				zap.Error(err))
			// Fail-closed: skip the whole subtree rather than silently falling
			// back to file-level detectors for a directory we already claimed.
			return fs.SkipDir
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

		*findings = append(*findings, dirFindings...)
		return fs.SkipDir
	}

	return nil
}

// handleFile dispatches a single file to the first matching Detector.
// Files inside directories claimed by a DirectoryAwareDetector are never
// passed here because WalkDir skips those subtrees via fs.SkipDir.
func (s *ScanService) handleFile(fsys fs.FS, filePath string, findings *[]domain.Finding) error {
	det := s.matchDetector(filePath)
	if det == nil {
		return nil
	}

	content, err := fs.ReadFile(fsys, filePath)
	if err != nil {
		s.log.Warn("cannot read file",
			zap.String("path", filePath),
			zap.String("detector", det.Name()),
			zap.Error(err))
		return nil
	}

	fileFindings, err := det.Detect(filePath, content)
	if err != nil {
		s.log.Warn("detector error",
			zap.String("path", filePath),
			zap.String("detector", det.Name()),
			zap.Error(err))
		// Use whatever partial findings the detector returned.
	}

	*findings = append(*findings, fileFindings...)
	return nil
}

// matchDetector returns the first registered Detector whose Match returns true
// for the given path, or nil if none match.
func (s *ScanService) matchDetector(filePath string) port.Detector {
	for _, det := range s.detectors {
		if det.Match(filePath) {
			return det
		}
	}
	return nil
}

// isExcluded reports whether path matches any of the provided doublestar
// glob patterns. Patterns must have been validated with validatePatterns first.
func (s *ScanService) isExcluded(p string, patterns []string) (bool, error) {
	for _, pattern := range patterns {
		matched, err := doublestar.Match(pattern, p)
		if err != nil {
			return false, fmt.Errorf("invalid glob pattern %q: %w", pattern, err)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
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
