# oci-image-detector

A production-ready CLI tool and GitHub Action that **recursively scans a directory tree for OCI / Docker image references** in:

| File type | Pattern |
|---|---|
| `Dockerfile` / `Containerfile` | `FROM` instructions (incl. `--platform`, `AS` aliases, `scratch`) |
| Helm `values.yaml` / `values-*.yaml` | `registry` + `repository` + `tag` fields |
| Terraform `*.tf` | `image` / `container` / `source` variable defaults, locals, and resource arguments |
| Any text file | Bare `<registry>/<image>:<tag>`, `<image>:<tag>`, and `@sha256:` references |

---

## Installation

### Pre-built binary

Download the archive for your platform from the [Releases](https://github.com/malachowski-labs/oci-image-detector/releases) page and put the binary on your `$PATH`.

```
oci-image-detector_linux_amd64.tar.gz
oci-image-detector_linux_arm64.tar.gz
oci-image-detector_darwin_amd64.tar.gz
oci-image-detector_darwin_arm64.tar.gz
oci-image-detector_windows_amd64.zip
```

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
| `--output-file` | `-o` | — | Write findings as JSON to this file (in addition to stdout) |
| `--allow-empty` | | `false` | Exit 0 when no image references are found |
| `--verbose` | `-v` | `false` | Enable debug logging on stderr |
| `--version` | | | Print version and exit |

### Examples

```bash
# Scan the current directory and print findings to stdout
oci-image-detector

# Scan a specific directory, exclude test fixtures, write JSON report
oci-image-detector -d ./infra -e 'test/**' -o findings.json

# Scan and exit 0 even when nothing is found (useful in pre-commit hooks)
oci-image-detector --allow-empty
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
| `1` | Scan error, or no findings found and `--allow-empty` not set |

---

## GitHub Action

```yaml
- uses: malachowski-labs/oci-image-detector@v0.3.0
  id: scan
  with:
    directory: ./infra
    exclude: |
      vendor/**
      test/**
    output-file: findings.json
    allow-empty: 'false'
    verbose: 'false'

- name: Print findings count
  run: echo "Found ${{ steps.scan.outputs.findings-count }} image references"
```

### Inputs

| Input | Default | Description |
|---|---|---|
| `version` | `latest` | Release tag to download, e.g. `v0.3.0` |
| `directory` | `.` | Root directory to scan |
| `exclude` | `''` | Newline-separated doublestar glob patterns to exclude |
| `output-file` | `''` | Path for the JSON report. If empty, a temp file is used and its path is available via `outputs.output-file` |
| `allow-empty` | `false` | Exit 0 when no image references are found |
| `verbose` | `false` | Enable debug logging |

### Outputs

| Output | Description |
|---|---|
| `findings-count` | Number of image references found |
| `output-file` | Absolute path to the JSON findings report |

### Required permissions

The action uses `${{ github.token }}` to resolve the release version and download the binary archive. The calling workflow needs **`contents: read`** on the token (this is the GitHub default, but organisations with restrictive default permissions must grant it explicitly):

```yaml
permissions:
  contents: read
```

### Prerequisites

The `findings-count` output is derived by running `jq` on the JSON report. `jq` is pre-installed on all GitHub-hosted runners (`ubuntu-*`, `macos-*`, `windows-*`). Self-hosted runners must provide `jq` independently.

### Full workflow example

```yaml
name: Image Audit

on:
  pull_request:

permissions:
  contents: read  # required by oci-image-detector to download the binary

jobs:
  audit:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4.2.2

      - uses: malachowski-labs/oci-image-detector@v0.3.0
        id: scan
        with:
          directory: .
          exclude: |
            .git/**
            vendor/**
          output-file: ${{ runner.temp }}/findings.json

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

## Supported platforms

The GitHub Action works on `ubuntu-*`, `macos-*`, and `windows-*` GitHub-hosted runners.

---

## License

[MIT](LICENSE)
