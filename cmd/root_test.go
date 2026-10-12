package cmd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/malachowski-labs/oci-image-detector/pkg/registry"
)

// runScan executes the root command against a directory holding a single
// Dockerfile and returns the JSON report it wrote.
func runScan(t *testing.T, args ...string) (string, error) {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM ghcr.io/org/app:v1\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	report := filepath.Join(t.TempDir(), "findings.json")

	cmd := newRootCmd("test")
	cmd.SetArgs(append([]string{"--directory", dir, "--output-file", report, "--allow-empty"}, args...))
	if err := cmd.Execute(); err != nil {
		return "", err
	}

	content, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	return string(content), nil
}

func TestDisableDetector_fileFallsThroughToFallback(t *testing.T) {
	enabled, err := runScan(t)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(enabled, `"strategy": "dockerfile"`) {
		t.Fatalf("expected a dockerfile finding by default, got: %s", enabled)
	}

	disabled, err := runScan(t, "--disable-detector", "dockerfile")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(disabled, `"strategy": "dockerfile"`) {
		t.Errorf("dockerfile detector still ran after being disabled: %s", disabled)
	}
	// Disabling a detector does not exclude its files: the fallback picks them up.
	if !strings.Contains(disabled, `"strategy": "generic"`) {
		t.Errorf("expected the fallback to handle the Dockerfile, got: %s", disabled)
	}
}

func TestDisableDetector_unknownName(t *testing.T) {
	_, err := runScan(t, "--disable-detector", "no-such-detector")
	if !errors.Is(err, registry.ErrUnknownDetector) {
		t.Errorf("error = %v, want %v", err, registry.ErrUnknownDetector)
	}
}

func TestReportError_failsTheCommand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM ghcr.io/org/app:v1\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// The parent directory of the report does not exist, so it cannot be written.
	report := filepath.Join(t.TempDir(), "missing", "findings.json")

	cmd := newRootCmd("test")
	cmd.SetArgs([]string{"--directory", dir, "--output-file", report})
	if err := cmd.Execute(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error = %v, want one wrapping %v", err, fs.ErrNotExist)
	}
}
