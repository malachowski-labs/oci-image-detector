package terraform_test

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/terraform"
)

func TestDetector_MatchDir(t *testing.T) {
	det := terraform.New()

	withTF := []fs.DirEntry{
		fakeDirEntry{name: "main.tf"},
		fakeDirEntry{name: "vars.tf"},
	}
	withoutTF := []fs.DirEntry{
		fakeDirEntry{name: "values.yaml"},
		fakeDirEntry{name: "Dockerfile"},
	}
	mixed := []fs.DirEntry{
		fakeDirEntry{name: "main.tf"},
		fakeDirEntry{name: "README.md"},
	}

	if !det.MatchDir("infra", withTF) {
		t.Error("expected MatchDir=true for dir with .tf files")
	}
	if det.MatchDir("app", withoutTF) {
		t.Error("expected MatchDir=false for dir without .tf files")
	}
	if !det.MatchDir("mixed", mixed) {
		t.Error("expected MatchDir=true for dir with at least one .tf file")
	}
}

func TestDetector_DetectDir_directString(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte(`
resource "aws_ecs_task_definition" "app" {
  container_definitions = jsonencode([{
    image = "nginx:1.25"
  }])
}
`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %v", len(findings), findings)
	}
	if findings[0].Ref.Raw != "nginx:1.25" {
		t.Errorf("Raw = %q, want %q", findings[0].Ref.Raw, "nginx:1.25")
	}
	if findings[0].Strategy != terraform.Strategy {
		t.Errorf("Strategy = %q, want %q", findings[0].Strategy, terraform.Strategy)
	}
}

func TestDetector_DetectDir_variableDefault(t *testing.T) {
	dir := fstest.MapFS{
		"variables.tf": {Data: []byte(`
variable "app_image" {
  default = "ghcr.io/org/app:v2.0.0"
}
`)},
		"main.tf": {Data: []byte(`
resource "aws_ecs_task_definition" "app" {
  image = var.app_image
}
`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.Ref.Raw == "ghcr.io/org/app:v2.0.0" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected finding for ghcr.io/org/app:v2.0.0, got: %v", findings)
	}
}

// TestDetector_DetectDir_variableDefaultWithNestedBlock verifies that variable
// blocks containing nested sub-blocks (e.g. validation {}) are still parsed
// correctly — the balanced-brace extractor must not stop at the inner '}'.
func TestDetector_DetectDir_variableDefaultWithNestedBlock(t *testing.T) {
	dir := fstest.MapFS{
		"variables.tf": {Data: []byte(`
variable "app_image" {
  description = "Container image URI"
  validation {
    condition     = length(var.app_image) > 0
    error_message = "Image must not be empty."
  }
  default = "ghcr.io/org/app:v3.0.0"
}
`)},
		"main.tf": {Data: []byte(`image = var.app_image`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.Ref.Raw == "ghcr.io/org/app:v3.0.0" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected finding for ghcr.io/org/app:v3.0.0, got: %v", findings)
	}
}

func TestDetector_DetectDir_tfvarsOverride(t *testing.T) {
	dir := fstest.MapFS{
		"variables.tf": {Data: []byte(`
variable "image_uri" {
  default = "nginx:latest"
}
`)},
		"terraform.tfvars": {Data: []byte(`image_uri = "nginx:1.25"`)},
		"main.tf":          {Data: []byte(`image = var.image_uri`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.Ref.Raw == "nginx:1.25" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected tfvars value nginx:1.25, got: %v", findings)
	}
}

func TestDetector_DetectDir_interpolation(t *testing.T) {
	dir := fstest.MapFS{
		"variables.tf": {Data: []byte(`
variable "image_tag" {
  default = "v1.0.0"
}
`)},
		"main.tf": {Data: []byte(`image = "ghcr.io/org/app:${var.image_tag}"`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.Ref.Raw == "ghcr.io/org/app:v1.0.0" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected resolved interpolation ghcr.io/org/app:v1.0.0, got: %v", findings)
	}
}

func TestDetector_DetectDir_unresolvableVarKeptAsRaw(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte(`image = "ghcr.io/org/app:${var.unknown_tag}"`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.Ref.Raw == "ghcr.io/org/app:${var.unknown_tag}" && !f.Ref.Parsed {
			found = true
		}
	}
	if !found {
		t.Errorf("expected raw-only finding for unresolvable var, got: %v", findings)
	}
}

// TestDetector_DetectDir_hyphenatedVarName verifies that HCL identifiers
// containing hyphens (e.g. var.base-image) are correctly resolved.
func TestDetector_DetectDir_hyphenatedVarName(t *testing.T) {
	dir := fstest.MapFS{
		"variables.tf": {Data: []byte(`
variable "base-image" {
  default = "ghcr.io/org/app:v1.0.0"
}
`)},
		"main.tf": {Data: []byte(`image = var.base-image`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.Ref.Raw == "ghcr.io/org/app:v1.0.0" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected finding for hyphenated var, got: %v", findings)
	}
}

// fakeDirEntry is a minimal fs.DirEntry implementation for tests.
type fakeDirEntry struct {
	name  string
	isDir bool
}

func (f fakeDirEntry) Name() string               { return f.name }
func (f fakeDirEntry) IsDir() bool                { return f.isDir }
func (f fakeDirEntry) Type() fs.FileMode          { return 0 }
func (f fakeDirEntry) Info() (fs.FileInfo, error) { return nil, nil }
