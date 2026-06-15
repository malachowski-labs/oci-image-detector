package service_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"go.uber.org/zap"

	"github.com/malachowski-labs/oci-image-detector/internal/domain"
	"github.com/malachowski-labs/oci-image-detector/internal/port"
	"github.com/malachowski-labs/oci-image-detector/internal/service"
)

// --- test doubles ---

// stubDetector matches files whose path has the given suffix.
type stubDetector struct {
	name    string
	suffix  string
	results []domain.Finding
	err     error
}

func (d *stubDetector) Name() string { return d.name }
func (d *stubDetector) Match(p string) bool {
	return len(p) >= len(d.suffix) && p[len(p)-len(d.suffix):] == d.suffix
}
func (d *stubDetector) Detect(p string, _ []byte) ([]domain.Finding, error) {
	out := make([]domain.Finding, len(d.results))
	for i, f := range d.results {
		f.FilePath = p
		out[i] = f
	}
	return out, d.err
}

// stubDirDetector claims directories whose path has the given suffix.
type stubDirDetector struct {
	name      string
	dirSuffix string
	results   []domain.Finding
	err       error
}

func (d *stubDirDetector) Name() string { return d.name }
func (d *stubDirDetector) MatchDir(dirPath string, _ []fs.DirEntry) bool {
	return len(dirPath) >= len(d.dirSuffix) &&
		dirPath[len(dirPath)-len(d.dirSuffix):] == d.dirSuffix
}
func (d *stubDirDetector) DetectDir(_ fs.FS) ([]domain.Finding, error) {
	return d.results, d.err
}

// --- helpers ---

func newService(dets []port.Detector, dirDets []port.DirectoryAwareDetector) *service.ScanService {
	return service.NewScanService(dets, dirDets, zap.NewNop())
}

func ref(raw string) domain.ImageRef { return domain.NewImageRef(raw) }

// --- tests ---

func TestScanFS_emptyFS(t *testing.T) {
	svc := newService(nil, nil)
	findings, err := svc.ScanFS(context.Background(), fstest.MapFS{}, port.ScanOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings, got %d", len(findings))
	}
}

func TestScanFS_fileDetector(t *testing.T) {
	fsys := fstest.MapFS{
		"Dockerfile": {Data: []byte("FROM nginx:1.25")},
		"README.md":  {Data: []byte("not a dockerfile")},
	}
	det := &stubDetector{
		name:    "dockerfile",
		suffix:  "Dockerfile",
		results: []domain.Finding{{Ref: ref("nginx:1.25"), Line: 1}},
	}
	findings, err := newService([]port.Detector{det}, nil).ScanFS(context.Background(), fsys, port.ScanOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].FilePath != "Dockerfile" {
		t.Errorf("FilePath = %q, want %q", findings[0].FilePath, "Dockerfile")
	}
}

func TestScanFS_firstDetectorWins(t *testing.T) {
	fsys := fstest.MapFS{"file.txt": {Data: []byte("data")}}
	first := &stubDetector{
		name:    "first",
		suffix:  ".txt",
		results: []domain.Finding{{Ref: ref("first:1")}},
	}
	second := &stubDetector{
		name:    "second",
		suffix:  ".txt",
		results: []domain.Finding{{Ref: ref("second:1")}},
	}
	findings, err := newService([]port.Detector{first, second}, nil).ScanFS(context.Background(), fsys, port.ScanOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding (first detector wins), got %d", len(findings))
	}
	if findings[0].Ref.Raw != "first:1" {
		t.Errorf("expected finding from first detector, got %q", findings[0].Ref.Raw)
	}
}

func TestScanFS_dirDetector_claimsDirectory(t *testing.T) {
	fsys := fstest.MapFS{
		"infra/main.tf":  {Data: []byte(`image = "nginx:latest"`)},
		"infra/vars.tf":  {Data: []byte(`variable "image" {}`)},
		"app/Dockerfile": {Data: []byte("FROM alpine:3")},
	}
	dirDet := &stubDirDetector{
		name:      "terraform",
		dirSuffix: "infra",
		results:   []domain.Finding{{Ref: ref("nginx:latest"), Line: 1, FilePath: "main.tf"}},
	}
	fileDet := &stubDetector{
		name:    "dockerfile",
		suffix:  "Dockerfile",
		results: []domain.Finding{{Ref: ref("alpine:3"), Line: 1}},
	}
	findings, err := newService([]port.Detector{fileDet}, []port.DirectoryAwareDetector{dirDet}).
		ScanFS(context.Background(), fsys, port.ScanOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d: %v", len(findings), findings)
	}
	var tfFound, dockerFound bool
	for _, f := range findings {
		switch f.FilePath {
		case "infra/main.tf":
			tfFound = true
		case "app/Dockerfile":
			dockerFound = true
		}
	}
	if !tfFound {
		t.Error("expected finding with FilePath infra/main.tf (dirPath prepended)")
	}
	if !dockerFound {
		t.Error("expected finding with FilePath app/Dockerfile")
	}
}

func TestScanFS_dirDetector_noDoubleReport(t *testing.T) {
	// The generic file detector also matches .tf — it must not fire because
	// fs.SkipDir prevents WalkDir from descending into the claimed directory.
	fsys := fstest.MapFS{
		"infra/main.tf": {Data: []byte(`image = "nginx:latest"`)},
	}
	dirDet := &stubDirDetector{
		name:      "terraform",
		dirSuffix: "infra",
		results:   []domain.Finding{{Ref: ref("nginx:latest"), Line: 1, FilePath: "main.tf"}},
	}
	fileDet := &stubDetector{
		name:    "generic",
		suffix:  ".tf",
		results: []domain.Finding{{Ref: ref("nginx:latest"), Line: 1}},
	}
	findings, err := newService([]port.Detector{fileDet}, []port.DirectoryAwareDetector{dirDet}).
		ScanFS(context.Background(), fsys, port.ScanOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Errorf("expected 1 finding (no double-report), got %d", len(findings))
	}
}

func TestScanFS_dirDetector_partialResultsOnError(t *testing.T) {
	fsys := fstest.MapFS{"infra/main.tf": {Data: []byte("x")}}
	dirDet := &stubDirDetector{
		name:      "terraform",
		dirSuffix: "infra",
		results:   []domain.Finding{{Ref: ref("nginx:latest"), FilePath: "main.tf"}},
		err:       errors.New("parse error"),
	}
	findings, err := newService(nil, []port.DirectoryAwareDetector{dirDet}).
		ScanFS(context.Background(), fsys, port.ScanOptions{})
	if err != nil {
		t.Fatalf("scan should not fail on detector error: %v", err)
	}
	// Partial findings from before the error must still be returned.
	if len(findings) != 1 {
		t.Errorf("expected 1 partial finding, got %d", len(findings))
	}
}

func TestScanFS_dirDetector_emptyFilePath_joined(t *testing.T) {
	// Adapter returns FilePath="" — path.Join must not produce a trailing slash.
	fsys := fstest.MapFS{"infra/main.tf": {Data: []byte("x")}}
	dirDet := &stubDirDetector{
		name:      "terraform",
		dirSuffix: "infra",
		results:   []domain.Finding{{Ref: ref("x:1"), FilePath: ""}},
	}
	findings, err := newService(nil, []port.DirectoryAwareDetector{dirDet}).
		ScanFS(context.Background(), fsys, port.ScanOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) == 1 && findings[0].FilePath != "infra" {
		t.Errorf("FilePath = %q, want %q", findings[0].FilePath, "infra")
	}
}

func TestScanFS_exclude_file(t *testing.T) {
	fsys := fstest.MapFS{
		"Dockerfile":        {Data: []byte("FROM nginx:1.25")},
		"vendor/Dockerfile": {Data: []byte("FROM alpine:3")},
	}
	det := &stubDetector{
		name:    "dockerfile",
		suffix:  "Dockerfile",
		results: []domain.Finding{{Ref: ref("x:1"), Line: 1}},
	}
	findings, err := newService([]port.Detector{det}, nil).ScanFS(
		context.Background(), fsys,
		// Use "vendor" to prune the directory itself (short-circuits WalkDir),
		// plus "vendor/**" to exclude any files if the dir pattern is not matched.
		port.ScanOptions{Exclude: []string{"vendor", "vendor/**"}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding (vendor excluded), got %d", len(findings))
	}
	if findings[0].FilePath != "Dockerfile" {
		t.Errorf("FilePath = %q, want root Dockerfile", findings[0].FilePath)
	}
}

func TestScanFS_exclude_directory_prunes_walk(t *testing.T) {
	// "vendor" (without /**) should match the directory entry and return
	// fs.SkipDir, preventing WalkDir from descending at all.
	fsys := fstest.MapFS{
		"vendor/Dockerfile": {Data: []byte("FROM alpine:3")},
	}
	det := &stubDetector{
		name:    "dockerfile",
		suffix:  "Dockerfile",
		results: []domain.Finding{{Ref: ref("alpine:3"), Line: 1}},
	}
	findings, err := newService([]port.Detector{det}, nil).ScanFS(
		context.Background(), fsys,
		port.ScanOptions{Exclude: []string{"vendor"}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected 0 findings (vendor dir pruned), got %d", len(findings))
	}
}

func TestScanFS_invalidExcludePattern(t *testing.T) {
	svc := newService(nil, nil)
	_, err := svc.ScanFS(context.Background(), fstest.MapFS{}, port.ScanOptions{
		Exclude: []string{"[invalid"},
	})
	if err == nil {
		t.Error("expected error for invalid glob pattern, got nil")
	}
}

func TestScanFS_resultsSorted(t *testing.T) {
	fsys := fstest.MapFS{
		"b.txt": {Data: []byte("b")},
		"a.txt": {Data: []byte("a")},
	}
	det := &stubDetector{
		name:    "generic",
		suffix:  ".txt",
		results: []domain.Finding{{Ref: ref("z:1"), Line: 1}},
	}
	findings, err := newService([]port.Detector{det}, nil).ScanFS(context.Background(), fsys, port.ScanOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := 1; i < len(findings); i++ {
		if findings[i-1].FilePath > findings[i].FilePath {
			t.Errorf("findings not sorted at index %d: %q > %q",
				i, findings[i-1].FilePath, findings[i].FilePath)
		}
	}
}

func TestScanFS_excludeImages(t *testing.T) {
	fsys := fstest.MapFS{
		"a.txt": {Data: []byte("ghcr.io/org/keep:v1\nlocalhost:5000/noise:v1\n")},
	}
	det := &stubDetector{
		name:   "generic",
		suffix: ".txt",
		results: []domain.Finding{
			{Ref: ref("ghcr.io/org/keep:v1"), Line: 1},
			{Ref: ref("localhost:5000/noise:v1"), Line: 2},
		},
	}
	findings, err := newService([]port.Detector{det}, nil).ScanFS(
		context.Background(), fsys,
		port.ScanOptions{ExcludeImages: []string{"localhost:5000/**"}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %v", len(findings), findings)
	}
	if findings[0].Ref.Raw != "ghcr.io/org/keep:v1" {
		t.Errorf("unexpected finding: %q", findings[0].Ref.Raw)
	}
}

func TestScanFS_excludeImages_invalidPattern(t *testing.T) {
	svc := newService(nil, nil)
	_, err := svc.ScanFS(context.Background(), fstest.MapFS{}, port.ScanOptions{
		ExcludeImages: []string{"[invalid"},
	})
	if err == nil {
		t.Error("expected error for invalid exclude-images pattern, got nil")
	}
}

func TestScanFS_contextCancellation(t *testing.T) {
	fsys := fstest.MapFS{
		"a.txt": {Data: []byte("a")},
		"b.txt": {Data: []byte("b")},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	svc := newService(nil, nil)
	_, err := svc.ScanFS(ctx, fsys, port.ScanOptions{})
	if err == nil {
		t.Error("expected context cancellation error, got nil")
	}
}
