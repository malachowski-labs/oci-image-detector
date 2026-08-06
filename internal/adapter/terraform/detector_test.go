package terraform_test

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/malachowski-labs/oci-image-detector/internal/adapter/terraform"
	"github.com/malachowski-labs/oci-image-detector/internal/domain"
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
    image = "ghcr.io/org/app:1.25"
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
	if findings[0].Ref.Raw != "ghcr.io/org/app:1.25" {
		t.Errorf("Raw = %q, want %q", findings[0].Ref.Raw, "ghcr.io/org/app:1.25")
	}
	if findings[0].Strategy != terraform.Strategy {
		t.Errorf("Strategy = %q, want %q", findings[0].Strategy, terraform.Strategy)
	}
}

// TestDetector_DetectDir_bareImageNotReported documents the precision contract:
// a bare library image without a registry host (e.g. "nginx:1.25") is too
// ambiguous to distinguish from arbitrary "word:word" strings in a .tf file,
// so the detector deliberately does not report it. Resolving such short forms
// is the job of detectors with structural context (Dockerfile, Helm).
func TestDetector_DetectDir_bareImageNotReported(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte(`image = "nginx:1.25"`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for bare image, got: %v", findings)
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
  default = "ghcr.io/org/app:latest"
}
`)},
		"terraform.tfvars": {Data: []byte(`image_uri = "ghcr.io/org/app:1.25"`)},
		"main.tf":          {Data: []byte(`image = var.image_uri`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.Ref.Raw == "ghcr.io/org/app:1.25" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected tfvars value ghcr.io/org/app:1.25, got: %v", findings)
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

// TestDetector_DetectDir_unresolvableVarNotReported documents that an image
// reference whose tag is an unresolvable template (e.g. "${var.unknown_tag}")
// is not reported. The strict candidate filter cannot validate a reference
// whose version component is still a placeholder, so such strings are dropped
// rather than emitted as raw findings.
func TestDetector_DetectDir_unresolvableVarNotReported(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte(`image = "ghcr.io/org/app:${var.unknown_tag}"`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for unresolvable template tag, got: %v", findings)
	}
}

// TestDetector_DetectDir_falsePositivesRejected locks in the fix for issue #34:
// IAM members, GCP resource paths, WIF principals, module sources, provider
// names, Kubernetes API versions and HCL/YAML fragments must not be reported
// as image references.
func TestDetector_DetectDir_falsePositivesRejected(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte(`
locals {
  member      = "serviceAccount:foo@my-project.iam.gserviceaccount.com"
  group       = "group:team@example.com"
  repo_path   = "projects/my-project/locations/europe/repositories/my-repo"
  principal   = "principalSet://iam.googleapis.com/pool/attribute.x:value"
  module_src  = "git::https://example.com/org/repo.git//modules/mod?ref=v1.0.0"
  role        = "roles/artifactregistry.reader"
  rel_module  = "../../modules/artifact-registry"
  provider    = "hashicorp/google"
  api_version = "core.gardener.cloud/v1beta1"
  k8s_api     = "apps/v1"
  chart_url   = "https://charts.external-secrets.io"
  oci_chart   = "oci://registry.example.com/project/charts/myapp"
  location    = var.enabled ? var.location : error("location required")
}
`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for false-positive strings, got: %v", findings)
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

// hasRaw reports whether any finding has the given Raw image reference.
func hasRaw(findings []domain.Finding, raw string) bool {
	for _, f := range findings {
		if f.Ref.Raw == raw {
			return true
		}
	}
	return false
}

// TestDetector_DetectDir_localsResolution verifies that a local built from
// multiple variables is resolved (local.* was not handled at all before the
// HCL rewrite).
func TestDetector_DetectDir_localsResolution(t *testing.T) {
	dir := fstest.MapFS{
		"variables.tf": {Data: []byte(`
variable "registry" { default = "ghcr.io" }
variable "tag"      { default = "v1.0.0" }
`)},
		"main.tf": {Data: []byte(`
locals {
  image = "${var.registry}/org/app:${var.tag}"
}
resource "aws_ecs_task_definition" "app" {
  image = local.image
}
`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasRaw(findings, "ghcr.io/org/app:v1.0.0") {
		t.Errorf("expected resolved local ghcr.io/org/app:v1.0.0, got: %v", findings)
	}
}

// TestDetector_DetectDir_chainedLocals verifies fixpoint evaluation of a local
// that references another local.
func TestDetector_DetectDir_chainedLocals(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte(`
variable "tag" { default = "2.1.0" }
locals {
  repo  = "ghcr.io/org/app"
  image = "${local.repo}:${var.tag}"
}
resource "x" "y" {
  image = local.image
}
`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasRaw(findings, "ghcr.io/org/app:2.1.0") {
		t.Errorf("expected chained-local ghcr.io/org/app:2.1.0, got: %v", findings)
	}
}

// TestDetector_DetectDir_jsonencode verifies that an image nested inside an ECS
// container_definitions jsonencode([...]) blob is found — jsonencode evaluates
// to a concrete JSON string the candidate filter can scan.
func TestDetector_DetectDir_jsonencode(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte(`
variable "tag" { default = "1.25" }
resource "aws_ecs_task_definition" "app" {
  container_definitions = jsonencode([{
    name  = "app"
    image = "ghcr.io/org/app:${var.tag}"
  }])
}
`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasRaw(findings, "ghcr.io/org/app:1.25") {
		t.Errorf("expected jsonencode image ghcr.io/org/app:1.25, got: %v", findings)
	}
}

// TestDetector_DetectDir_functionCall verifies a supported stdlib function
// (format) is evaluated when assembling an image reference.
func TestDetector_DetectDir_functionCall(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte(`
variable "tag" { default = "3.0.0" }
resource "x" "y" {
  image = format("ghcr.io/org/app:%s", var.tag)
}
`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasRaw(findings, "ghcr.io/org/app:3.0.0") {
		t.Errorf("expected format() image ghcr.io/org/app:3.0.0, got: %v", findings)
	}
}

// TestDetector_DetectDir_tfvarsOnlyNoDefault verifies a variable with no default
// is resolved purely from a .tfvars assignment.
func TestDetector_DetectDir_tfvarsOnlyNoDefault(t *testing.T) {
	dir := fstest.MapFS{
		"variables.tf":     {Data: []byte(`variable "image_uri" {}`)},
		"terraform.tfvars": {Data: []byte(`image_uri = "ghcr.io/org/app:3.3.3"`)},
		"main.tf":          {Data: []byte(`image = var.image_uri`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasRaw(findings, "ghcr.io/org/app:3.3.3") {
		t.Errorf("expected tfvars-only ghcr.io/org/app:3.3.3, got: %v", findings)
	}
}

// TestDetector_DetectDir_unresolvedResourceRefSkipped documents the precision
// contract under HCL: an image sourced from a resource attribute cannot be
// resolved statically and must not be reported.
func TestDetector_DetectDir_unresolvedResourceRefSkipped(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte(`
resource "aws_ecs_task_definition" "app" {
  image = aws_ecr_repository.app.repository_url
}
`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for unresolved resource ref, got: %v", findings)
	}
}

// TestDetector_DetectDir_suppressAnnotation verifies that an attribute whose
// source line carries the inline suppress annotation is not reported.
func TestDetector_DetectDir_suppressAnnotation(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte("resource \"x\" \"y\" {\n" +
			"  image = \"ghcr.io/org/app:1.0\" # oci-image-detector:ignore\n" +
			"}\n")},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for suppressed line, got: %v", findings)
	}
}

// TestDetector_DetectDir_lineNumbers verifies findings carry the real source
// line of the attribute (the HCL parser exposes accurate ranges).
func TestDetector_DetectDir_lineNumbers(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte("resource \"x\" \"y\" {\n  image = \"ghcr.io/org/app:1.0\"\n}\n")},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %v", len(findings), findings)
	}
	if findings[0].Line != 2 {
		t.Errorf("Line = %d, want 2", findings[0].Line)
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
