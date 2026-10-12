# oci-image-detector

A production-ready CLI tool and GitHub Action that **recursively scans a directory tree for OCI / Docker image references** in:

| File type | Pattern |
|---|---|
| `Dockerfile` / `Containerfile` | `FROM` instructions (incl. `--platform`, `AS` aliases, `scratch`) |
| Helm `values.yaml` and per-environment variants (`values-*.yaml`, `values.*.yaml`, `*-values.yaml`, `*.values.yaml`, incl. `.yml`) | `registry` + `repository` + `tag` / `digest` blocks and pinned strings (`image: nginx:1.25`) under image-shaped keys, incl. short forms; fully-qualified references in any other value |
| Docker Compose `compose.yaml` / `docker-compose.yaml` (incl. `.yml` and `compose.<suffix>.yaml` overrides) | `services.<name>.image` and `docker-image://` additional build contexts, incl. short forms and images shared through YAML anchors and merge keys; fully-qualified references in any other value |
| Kubernetes manifests: any `*.yaml` / `*.yml` with an `apiVersion` and `kind` | `image` of every `containers` / `initContainers` / `ephemeralContainers` entry, incl. short forms; fully-qualified references in any other value |
| Terraform `*.tf` | `image` / `container` / `source` variable defaults, locals, and resource arguments |
| GitHub Actions `*.yml` / `*.yaml` under `.github/workflows/` and `.github/actions/` | `uses: docker://image:tag` steps, `container.image`, `services.<name>.image`, `runs.image: docker://image:tag` of a container action, incl. short forms; fully-qualified references in any other value |
| Any text file | Bare `<registry>/<image>:<tag>`, `<image>:<tag>`, and `@sha256:` references |

---

## Installation

### Docker (recommended)

```bash
docker pull ghcr.io/malachowski-labs/oci-image-detector:latest
```

### Pre-built binary

Download the archive for your platform from the [Releases](https://github.com/malachowski-labs/oci-image-detector/releases) page and put the binary on your `$PATH`.

```
oci-image-detector_linux_amd64.tar.gz
oci-image-detector_linux_arm64.tar.gz
oci-image-detector_darwin_amd64.tar.gz
oci-image-detector_darwin_arm64.tar.gz
oci-image-detector_windows_amd64.zip
```

Each release also includes `sha256sums.txt` for integrity verification.

### From source

Requires **Go 1.25+**.

```bash
go install github.com/malachowski-labs/oci-image-detector@latest
```

---

## CLI usage

```
oci-image-detector [flags]
```

### Flags

| Flag | Short | Default | Description |
|---|---|---|---|
| `--directory` | `-d` | `.` | Root directory to scan |
| `--exclude` | `-e` | — | Glob pattern to exclude (repeatable, doublestar syntax). Always-excluded: `.git/**`, `go.sum` |
| `--exclude-images` | | — | Glob pattern matched against the image reference as written in the source to drop findings (repeatable, doublestar syntax, e.g. `localhost:5000/**`) |
| `--disable-detector` | | — | Detector to turn off by name (repeatable). See [Disabling detectors](#disabling-detectors) |
| `--output-file` | `-o` | — | Write findings as JSON to this file (in addition to stdout) |
| `--allow-empty` | | `false` | Exit 0 when no image references are found |
| `--verbose` | `-v` | `false` | Enable debug logging on stderr |
| `--version` | | | Print version and exit |

### Binary examples

```bash
# Scan the current directory and print findings to stdout
oci-image-detector

# Scan a specific directory, exclude test fixtures, write JSON report
oci-image-detector -d ./infra -e 'test/**' -o findings.json

# Scan and exit 0 even when nothing is found (useful in pre-commit hooks)
oci-image-detector --allow-empty
```

### Docker examples

```bash
# Scan the current directory
docker run --rm \
  -v "$(pwd):/workspace:ro" \
  ghcr.io/malachowski-labs/oci-image-detector:latest \
  --directory /workspace

# Write a JSON report to the host
docker run --rm \
  -v "$(pwd):/workspace:ro" \
  -v "$(pwd)/out:/out" \
  ghcr.io/malachowski-labs/oci-image-detector:latest \
  --directory /workspace \
  --output-file /out/findings.json

# Exclude vendor, exit 0 when nothing found
docker run --rm \
  -v "$(pwd):/workspace:ro" \
  ghcr.io/malachowski-labs/oci-image-detector:latest \
  --directory /workspace \
  --exclude 'vendor/**' \
  --allow-empty
```

### Output

Human-readable table on **stdout**:

```
FILE                            IMAGE
infra/Dockerfile                nginx:1.27.0
infra/helm/values.yaml          registry.example.com/app:v2.1.3
infra/terraform/main.tf         hashicorp/consul:1.18.0
```

JSON report (when `--output-file` is set):

```json
{
  "findings": [
    {
      "file_path": "infra/Dockerfile",
      "line": 1,
      "strategy": "dockerfile",
      "ref": {
        "raw": "nginx:1.27.0",
        "canonical": "index.docker.io/library/nginx:1.27.0",
        "registry": "index.docker.io",
        "repository": "library/nginx",
        "tag": "1.27.0",
        "parsed": true
      }
    }
  ]
}
```

`findings` is always an array — never `null` — making it safe to iterate in scripts without a null-check.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Scan completed; findings present (or `--allow-empty` set) |
| `1` | Scan or report error, or no findings found and `--allow-empty` not set |

---

## GitHub Action

The action runs the same Docker image used locally — no extra permissions required.

```yaml
- uses: malachowski-labs/oci-image-detector@v1.4.0
  id: scan
  with:
    directory: ./infra
    exclude: |
      vendor/**
      test/**

- name: Print findings count
  run: echo "Found ${{ steps.scan.outputs.findings-count }} image references"
```

### Inputs

| Input | Default | Description |
|---|---|---|
| `version` | `latest` | Docker image tag to run, e.g. `v1.4.0` |
| `directory` | `.` | Root directory to scan (relative to repo root) |
| `exclude` | `''` | Newline-separated doublestar glob patterns to exclude |
| `exclude-images` | `''` | Newline-separated doublestar glob patterns matched against the image reference as written in the source (e.g. `localhost:5000/**`) |
| `disable-detector` | `''` | Newline-separated names of detectors to turn off. See [Disabling detectors](#disabling-detectors) |
| `allow-empty` | `false` | Exit 0 when no image references are found |
| `verbose` | `false` | Enable debug logging |

### Outputs

| Output | Description |
|---|---|
| `findings-count` | Number of image references found |
| `output-file` | Absolute path to the JSON findings report |

### Prerequisites

- Docker must be available on the runner. All GitHub-hosted `ubuntu-*` runners include Docker; `macos-*` and `windows-*` runners do not have Docker by default.
- `jq` is used to count findings and is pre-installed on all GitHub-hosted runners. Self-hosted runners must provide it independently.

### Full workflow example

```yaml
name: Image Audit

on:
  pull_request:

jobs:
  audit:
    runs-on: ubuntu-latest   # Docker required — use ubuntu-* runners
    steps:
      - uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4.2.2

      - uses: malachowski-labs/oci-image-detector@v1.4.0
        id: scan
        with:
          directory: .
          exclude: |
            vendor/**

      - name: Upload findings
        uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2
        with:
          name: oci-findings
          path: ${{ steps.scan.outputs.output-file }}

      - name: Warn if no images found
        if: steps.scan.outputs.findings-count == '0'
        run: echo "::warning::No OCI image references detected"
```

---

## Suppressing findings

Two complementary mechanisms let you silence specific findings without excluding entire files.

### Inline annotation

Add `oci-image-detector:ignore` in a comment next to the image reference. Where the comment has to sit depends on the file type:

| File type | Annotation placement |
|---|---|
| Any text file | Anywhere on the line holding the reference |
| Terraform | On the first line of the attribute, even when the literal sits on a later line of a multi-line expression |
| Helm values | YAML comment on any line of the image block (`registry:`, `repository:`, `tag:` or `digest:`), or on the value holding the reference |
| GitHub Actions | YAML comment on the value holding the reference, or on the reference's own line inside a block scalar |
| Docker Compose | YAML comment on the value holding the reference, or on the reference's own line inside a block scalar |
| Kubernetes | YAML comment on the value holding the reference, or on the reference's own line inside a block scalar |
| Dockerfile | Not supported — use [`--exclude-images`](#--exclude-images--exclude-images-input) |

```go
const img = "localhost:5000/example:v1" // oci-image-detector:ignore
```

```hcl
image = "localhost:5000/example:v1"  # oci-image-detector:ignore
```

```yaml
# Helm values
image:
  repository: localhost:5000/example  # oci-image-detector:ignore
  tag: v1
```

```yaml
# GitHub Actions
container:
  image: localhost:5000/example:v1  # oci-image-detector:ignore
```

> **Terraform:** annotating a variable `default` also suppresses every finding produced by uses of that variable.

> **Dockerfile:** Docker only treats `#` as a comment at the start of a line, so a trailing annotation on a `FROM` line breaks the build, and comment-only lines are not read by the detector.

### `--exclude-images` / `exclude-images` input

Filter findings by image reference using [doublestar](https://github.com/bmatcuk/doublestar) glob patterns. Useful when the same placeholder image appears across many files, when the source is generated or vendored and cannot be edited, or for Dockerfiles.

Patterns are matched against the reference **as written in the source** (`ref.raw` in the JSON report), not the normalised form (`ref.canonical`). A bare `nginx:1.25` is matched by `nginx:*`, not by `docker.io/**` or `index.docker.io/**`.

**CLI:**

```bash
oci-image-detector \
  --exclude-images 'localhost:5000/**' \
  --exclude-images '*.example.com/**'
```

**GitHub Action:**

```yaml
- uses: malachowski-labs/oci-image-detector@v1.4.0
  with:
    exclude-images: |
      localhost:5000/**
      *.example.com/**
```

---

## Disabling detectors

Each detector can be turned off by name: `compose`, `dockerfile`, `generic`, `githubactions`, `helm`, `kubernetes`, `terraform`. An unknown name is an error, so a typo cannot leave a detector on by accident.

The flag and the action input are available in releases after `v1.4.0`.

**CLI:**

```sh
oci-image-detector \
  --disable-detector generic \
  --disable-detector helm
```

**GitHub Action:**

```yaml
- uses: malachowski-labs/oci-image-detector@<version>
  with:
    disable-detector: |
      generic
      helm
```

Disabling a detector does not exclude its files. With `dockerfile` off, a Dockerfile is no longer parsed as one, but the `generic` detector still scans it as plain text and reports the fully-qualified references it finds. To skip files altogether, use `--exclude`.

## Custom detectors

Detectors are plugins compiled into the binary. To add one for a format the built-in set does not cover, build your own binary: write a detector package, register it from `init`, and reuse this project's command.

A file-level detector implements [`port.Detector`](pkg/port/detector.go) and declares a tier — `port.TierSpecialist` for a detector that claims one file format, `port.TierFallback` for one that takes whatever no specialist claimed. Specialists are always consulted before fallbacks, and the first match wins. A format that cannot be recognised by path also implements `port.ContentMatcher`; such a detector is consulted after the specialists that claim by path, and only claims files whose content it accepts. A detector that needs a whole directory at once implements `port.DirectoryAwareDetector` and registers with `registry.MustRegisterDir`. Its `Owns` method names the files of its format: those are never handed to a file-level detector, while the other files of the directory and its subdirectories are scanned as usual.

```go
package nomad

import (
	"path"
	"regexp"

	"github.com/malachowski-labs/oci-image-detector/pkg/domain"
	"github.com/malachowski-labs/oci-image-detector/pkg/imageref"
	"github.com/malachowski-labs/oci-image-detector/pkg/port"
	"github.com/malachowski-labs/oci-image-detector/pkg/registry"
)

var imageRe = regexp.MustCompile(`(?m)^\s*image\s*=\s*"([^"]+)"`)

type Detector struct{}

func init() { registry.MustRegister(&Detector{}) }

func (d *Detector) Name() string        { return "nomad" }
func (d *Detector) Tier() port.Tier     { return port.TierSpecialist }
func (d *Detector) Match(p string) bool { return path.Ext(p) == ".nomad" }

func (d *Detector) Detect(p string, content []byte) ([]domain.Finding, error) {
	var findings []domain.Finding
	for _, m := range imageRe.FindAllSubmatch(content, -1) {
		findings = append(findings, domain.Finding{
			Ref:      imageref.Parse(string(m[1])),
			FilePath: p,
			Strategy: "nomad",
		})
	}
	return findings, nil
}
```

Link it in by importing the package for its side effect:

```go
package main

import (
	"github.com/malachowski-labs/oci-image-detector/cmd"

	_ "example.com/custom/nomad"
)

func main() { cmd.Execute("custom") }
```

Detector names must be unique across the binary, including the built-in ones (`compose`, `dockerfile`, `generic`, `githubactions`, `helm`, `kubernetes`, `terraform`); a duplicate name stops the binary at start-up.

Two helper packages are available to detectors:

- [`pkg/imageref`](pkg/imageref/parse.go) — `Parse` resolves a reference into registry, repository and tag or digest; `Candidates` is the precision filter for scanning unstructured text, and `IsSourceFile` drops the `file.go:47` look-alikes it lets through. `ContainsTemplate` tells an unresolved placeholder (`${TAG}`, `{{ .Values.tag }}`) from a string that is simply not a reference.
- [`pkg/suppress`](pkg/suppress/annotation.go) — `IsIgnoredLine` recognises the [inline annotation](#inline-annotation), so a custom detector can honour it the same way the built-in ones do.

## Supported platforms

| Usage | Platforms |
|---|---|
| Docker image | `linux/amd64`, `linux/arm64` |
| Pre-built binary | Linux, macOS, Windows (amd64 + arm64 where applicable) |
| GitHub Action | `ubuntu-*` GitHub-hosted runners (Docker required) |

---

## License

[MIT](LICENSE)
