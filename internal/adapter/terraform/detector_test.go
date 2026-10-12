package terraform_test

import (
	"fmt"
	"io/fs"
	"strings"
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

// hasFinding reports whether any finding has the given Raw image reference at
// the given line.
func hasFinding(findings []domain.Finding, raw string, line uint) bool {
	for _, f := range findings {
		if f.Ref.Raw == raw && f.Line == line {
			return true
		}
	}
	return false
}

// TestDetector_DetectDir_partiallyResolvable verifies that a reference which
// cannot be resolved statically does not hide the literal images that share an
// attribute with it.
func TestDetector_DetectDir_partiallyResolvable(t *testing.T) {
	tests := []struct {
		name string
		src  string
		line uint
	}{
		{
			name: "jsonencode with resource ref",
			src: `resource "aws_ecs_task_definition" "app" {
  container_definitions = jsonencode([{
    image         = "ghcr.io/org/app:1.0"
    executionRole = aws_iam_role.exec.arn
  }])
}
`,
			line: 3,
		},
		{
			name: "heredoc with resource ref",
			src: `resource "aws_ecs_task_definition" "app" {
  container_definitions = <<-JSON
    [{
      "image": "ghcr.io/org/app:1.0",
      "logGroup": "${aws_cloudwatch_log_group.x.name}"
    }]
  JSON
}
`,
			line: 4,
		},
		{
			name: "list with resource ref",
			src: `locals {
  images = ["ghcr.io/org/app:1.0", aws_ecr_repository.r.repository_url]
}
`,
			line: 2,
		},
		{
			name: "function call with resource ref",
			src: `locals {
  image = coalesce(aws_ecr_repository.r.repository_url, "ghcr.io/org/app:1.0")
}
`,
			line: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := fstest.MapFS{"main.tf": {Data: []byte(tt.src)}}
			findings, err := terraform.New().DetectDir(dir)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(findings) != 1 || !hasFinding(findings, "ghcr.io/org/app:1.0", tt.line) {
				t.Errorf("expected ghcr.io/org/app:1.0 at line %d, got: %v", tt.line, findings)
			}
		})
	}
}

// TestDetector_DetectDir_unresolvedInterpolationNotStitched verifies that the
// text on either side of an unresolvable interpolation is never joined into a
// reference that does not exist in the source.
func TestDetector_DetectDir_unresolvedInterpolationNotStitched(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte(`image = "ghcr.io/org/${aws_ecr_repository.r.name}app:1.0"`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings across an unresolved interpolation, got: %v", findings)
	}
}

// TestDetector_DetectDir_syntaxError verifies that a file with a syntax error
// still yields the images it contains, that its siblings are unaffected, and
// that the parse failure is surfaced through the returned error.
func TestDetector_DetectDir_syntaxError(t *testing.T) {
	dir := fstest.MapFS{
		"broken.tf": {Data: []byte(`resource "x" "y" {
  image = "ghcr.io/org/app:1.0"
}
resource "x" "z" {
  oops = = 1
  image = "ghcr.io/org/other:2.0"
`)},
		"main.tf": {Data: []byte(`image = "ghcr.io/org/sibling:3.0"`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err == nil {
		t.Error("expected the parse failure to be returned as an error")
	}
	for _, want := range []struct {
		raw  string
		line uint
	}{
		{"ghcr.io/org/app:1.0", 2},
		{"ghcr.io/org/other:2.0", 6},
		{"ghcr.io/org/sibling:3.0", 1},
	} {
		if !hasFinding(findings, want.raw, want.line) {
			t.Errorf("expected %s at line %d, got: %v", want.raw, want.line, findings)
		}
	}
	if len(findings) != 3 {
		t.Errorf("expected 3 findings without duplicates, got %d: %v", len(findings), findings)
	}
}

// TestDetector_DetectDir_syntaxErrorHonorsSuppression verifies the line-based
// fallback for malformed files still honors the inline suppress annotation.
func TestDetector_DetectDir_syntaxErrorHonorsSuppression(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte("x = = 1\nimage = \"ghcr.io/org/app:1.0\" # oci-image-detector:ignore\n")},
	}
	findings, _ := terraform.New().DetectDir(dir)
	if len(findings) != 0 {
		t.Errorf("expected no findings for suppressed line, got: %v", findings)
	}
}

// TestDetector_DetectDir_readError verifies that an unreadable file is reported
// through the returned error without discarding its siblings' findings.
func TestDetector_DetectDir_readError(t *testing.T) {
	dir := unreadableFS{
		FS: fstest.MapFS{
			"bad.tf":  {Data: []byte(`image = "ghcr.io/org/bad:1.0"`)},
			"main.tf": {Data: []byte(`image = "ghcr.io/org/app:1.0"`)},
		},
		name: "bad.tf",
	}
	findings, err := terraform.New().DetectDir(dir)
	if err == nil {
		t.Error("expected the read failure to be returned as an error")
	}
	if len(findings) != 1 || !hasRaw(findings, "ghcr.io/org/app:1.0") {
		t.Errorf("expected only ghcr.io/org/app:1.0, got: %v", findings)
	}
}

// TestDetector_DetectDir_suppressInnerLine verifies that an annotation on an
// inner line of a multi-line attribute suppresses only the image on that line.
func TestDetector_DetectDir_suppressInnerLine(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte(`locals {
  images = [
    "ghcr.io/org/keep:1.0",
    "ghcr.io/org/drop:1.0", # oci-image-detector:ignore
  ]
}
`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 || !hasFinding(findings, "ghcr.io/org/keep:1.0", 3) {
		t.Errorf("expected only ghcr.io/org/keep:1.0 at line 3, got: %v", findings)
	}
}

// TestDetector_DetectDir_suppressedDefinition verifies that an image suppressed
// where it is defined is not reported where the local or variable is used.
func TestDetector_DetectDir_suppressedDefinition(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte(`variable "var_image" {
  default = "ghcr.io/org/var:1.0" # oci-image-detector:ignore
}
locals {
  local_image = "ghcr.io/org/local:1.0" # oci-image-detector:ignore
}
resource "x" "y" {
  a = local.local_image
  b = var.var_image
  c = "${local.local_image}-debug"
}
`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for suppressed definitions, got: %v", findings)
	}
}

// TestDetector_DetectDir_multiLineLineNumbers verifies that an image inside a
// multi-line attribute is reported at its own line, not the attribute's first.
func TestDetector_DetectDir_multiLineLineNumbers(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte(`variable "tag" { default = "1.0" }
resource "aws_ecs_task_definition" "app" {
  container_definitions = jsonencode([{
    name  = "app"
    image = "ghcr.io/org/app:${var.tag}"
  }])
  other = <<YAML
first line
image: ghcr.io/org/heredoc:${var.tag}
YAML
}
`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasFinding(findings, "ghcr.io/org/app:1.0", 5) {
		t.Errorf("expected ghcr.io/org/app:1.0 at line 5, got: %v", findings)
	}
	if !hasFinding(findings, "ghcr.io/org/heredoc:1.0", 9) {
		t.Errorf("expected ghcr.io/org/heredoc:1.0 at line 9, got: %v", findings)
	}
	if len(findings) != 2 {
		t.Errorf("expected 2 findings, got %d: %v", len(findings), findings)
	}
}

// TestDetector_DetectDir_conditionalBranches verifies that both branches of a
// conditional are reported, whichever one the available values select.
func TestDetector_DetectDir_conditionalBranches(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte(`variable "prod" { default = true }
locals {
  image = var.prod ? "ghcr.io/org/app:stable" : "ghcr.io/org/app:edge"
}
`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasRaw(findings, "ghcr.io/org/app:stable") || !hasRaw(findings, "ghcr.io/org/app:edge") {
		t.Errorf("expected both conditional branches, got: %v", findings)
	}
}

// TestDetector_DetectDir_functions verifies the functions whose behavior must
// match Terraform's rather than go-cty's stdlib, and those commonly used to
// pick an image out of a structure.
func TestDetector_DetectDir_functions(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{"replace with regex", `"ghcr.io/org/app:${replace("v1.2.3", "/^v/", "")}"`, "ghcr.io/org/app:1.2.3"},
		{"replace literal", `replace("ghcr.io/org/app:TAG", "TAG", "1.0")`, "ghcr.io/org/app:1.0"},
		{"coalesce skips empty string", `coalesce("", "ghcr.io/org/fallback:1.0")`, "ghcr.io/org/fallback:1.0"},
		{"lookup without default", `lookup({ app = "ghcr.io/org/app:1.0" }, "app")`, "ghcr.io/org/app:1.0"},
		{"lookup with default", `lookup({}, "app", "ghcr.io/org/default:1.0")`, "ghcr.io/org/default:1.0"},
		{"try", `try(var.missing.image, "ghcr.io/org/try:1.0")`, "ghcr.io/org/try:1.0"},
		{"can", `format("ghcr.io/org/can:%s", can(var.missing) ? "yes" : "no")`, "ghcr.io/org/can:no"},
		{"tostring", `"ghcr.io/org/app:${tostring(7)}"`, "ghcr.io/org/app:7"},
		{"regex", `"ghcr.io/org/app:${regex("[0-9.]+", "release-1.2.3")}"`, "ghcr.io/org/app:1.2.3"},
		{"jsondecode", `jsondecode("{\"image\":\"ghcr.io/org/json:1.0\"}").image`, "ghcr.io/org/json:1.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := fstest.MapFS{"main.tf": {Data: []byte("image = " + tt.expr + "\n")}}
			findings, err := terraform.New().DetectDir(dir)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !hasRaw(findings, tt.want) {
				t.Errorf("expected %s, got: %v", tt.want, findings)
			}
		})
	}
}

// TestDetector_DetectDir_replaceRegexNotReportedVerbatim verifies the detector
// does not report the pre-replacement tag when replace is given a regex.
func TestDetector_DetectDir_replaceRegexNotReportedVerbatim(t *testing.T) {
	dir := fstest.MapFS{
		"main.tf": {Data: []byte(`variable "ver" { default = "v1.2.3" }
locals {
  image = "ghcr.io/org/app:${replace(var.ver, "/^v/", "")}"
}
`)},
	}
	findings, err := terraform.New().DetectDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 || !hasRaw(findings, "ghcr.io/org/app:1.2.3") {
		t.Errorf("expected only ghcr.io/org/app:1.2.3, got: %v", findings)
	}
}

// TestDetector_DetectDir_evaluationLimits verifies that input describing a
// value far larger than itself is not expanded: the scan stays small, the
// literal image is still found, and the limit is surfaced through the error.
func TestDetector_DetectDir_evaluationLimits(t *testing.T) {
	var doubling strings.Builder
	doubling.WriteString("locals {\n  l0 = \"ghcr.io/org/app:1.0 \"\n")
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&doubling, "  l%d = \"${local.l%d}${local.l%d}\"\n", i, i-1, i-1)
	}
	doubling.WriteString("}\n")

	list := "[" + strings.Repeat(`"ghcr.io/org/app:1.0",`, 40) + "]"
	nested := fmt.Sprintf("x = [for a in %[1]s : [for b in %[1]s : [for c in %[1]s : [for d in %[1]s : [for e in %[1]s : a]]]]]\n", list)

	tests := []struct {
		name string
		src  string
	}{
		{"locals doubling each other", doubling.String()},
		{"nested for expressions", nested},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := fstest.MapFS{"main.tf": {Data: []byte(tt.src)}}
			findings, err := terraform.New().DetectDir(dir)
			if err == nil {
				t.Error("expected the evaluation limit to be returned as an error")
			}
			if !hasRaw(findings, "ghcr.io/org/app:1.0") {
				t.Errorf("expected the literal image to still be found, got %d findings", len(findings))
			}
			if len(findings) > 100_000 {
				t.Errorf("expected findings to stay bounded, got %d", len(findings))
			}
		})
	}
}

// unreadableFS wraps an fs.FS and fails every attempt to open one named file.
type unreadableFS struct {
	fs.FS
	name string
}

func (u unreadableFS) Open(name string) (fs.File, error) {
	if name == u.name {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return u.FS.Open(name)
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
