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
| `1` | Scan error, or no findings found and `--allow-empty` not set |

---

## GitHub Action

The action runs the same Docker image used locally — no extra permissions required.

```yaml
- uses: malachowski-labs/oci-image-detector@v0.5.0
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
| `version` | `latest` | Docker image tag to run, e.g. `v0.5.0` |
| `directory` | `.` | Root directory to scan (relative to repo root) |
| `exclude` | `''` | Newline-separated doublestar glob patterns to exclude |
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

      - uses: malachowski-labs/oci-image-detector@v0.5.0
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

## Supported platforms

| Usage | Platforms |
|---|---|
| Docker image | `linux/amd64`, `linux/arm64` |
| Pre-built binary | Linux, macOS, Windows (amd64 + arm64 where applicable) |
| GitHub Action | `ubuntu-*` GitHub-hosted runners (Docker required) |

---

## License

[MIT](LICENSE)
