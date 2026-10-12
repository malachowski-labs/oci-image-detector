# 2. Kubernetes manifests: claim files by content, through an optional port

- Status: Accepted
- Date: 2026-10-12
- Deciders: project maintainers
- Related: [#26](https://github.com/malachowski-labs/oci-image-detector/issues/26), [ADR-0001](0001-dockerfile-parsing-no-buildkit.md)

## Context

The generic detector only reports fully-qualified references. Short forms
(`nginx:1.25`, `org/app:v2`, `busybox`) are left to the specialist detectors,
which know from the file's structure that a string is an image. No specialist
covered Kubernetes manifests, so short-form container images in them were
silently missed.

Every other specialist claims its files by path: `Dockerfile`, `values.yaml`,
`compose.yaml`, anything under the GitHub workflow directories. Kubernetes
manifests follow no naming convention. `port.Detector.Match` sees only the
path, so a Kubernetes detector cannot tell a manifest from any other YAML file.

## Options considered

1. **Claim every `.yaml` / `.yml` by path.** The detector would take every YAML
   file away from the generic detector, including the many that are not
   manifests, and its claim would overlap the Helm, Compose and GitHub Actions
   detectors. Rejected.
2. **Let `Detect` decline.** The detector claims every YAML file by path and
   returns a sentinel error when the file is not a manifest, so the scan falls
   through to the next detector. This changes the meaning of `Detect` for every
   detector and still overlaps the other YAML specialists by path. Rejected.
3. **An optional content check next to `Match`.** Chosen.

## Decision

`pkg/port` gains an optional interface:

```go
type ContentMatcher interface {
    MatchContent(path string, content []byte) bool
}
```

A detector that implements it claims a file only when both `Match` and
`MatchContent` accept it. `Match` remains the cheap path filter, so the content
check runs for few files.

The scan service picks the detector for a file in this order:

1. the first specialist that claims by path alone;
2. the first `ContentMatcher` whose `MatchContent` accepts the content;
3. the first fallback.

A claim by path therefore always wins over a claim by content, whatever the
detectors are called. The existing detectors are unchanged and the interface is
additive, so this is not a breaking change.

The Kubernetes detector, following the dependency stance of ADR-0001, parses
with `gopkg.in/yaml.v3` and no Kubernetes libraries:

- **A manifest** is a `.yaml` / `.yml` file, outside the GitHub workflow
  directories, that parses and has at least one document with a top-level
  `apiVersion` and `kind`.
- **Container images** are the `image` of every entry of a `containers`,
  `initContainers` or `ephemeralContainers` list, at any depth of such a
  document. They are reported as written, short forms included.
- **Every other string value** of the file is checked with the strict
  `imageref.Candidates` filter the generic detector uses.

## Consequences

Positive:

- Short-form and tag-less container images in manifests are found.
- Looking for the three lists at any depth covers Pods, workloads, CronJobs
  and custom resources that embed a pod template, without a table of kinds.
- Fully-qualified references outside the container fields (`env` values,
  `args`, annotations, ConfigMap data) keep being reported, so claiming a file
  from the generic detector loses none of them.

Negative / accepted limitations:

- A claimed manifest is parsed twice, once to match and once to detect.
- References that appear only in YAML comments of a manifest are no longer
  reported; the generic detector reported them because it reads lines.
- Formats that hold images under other keys are only covered for
  fully-qualified references: Kustomize `images:` overrides, and custom
  resources with a singular `container` or a `steps` list.
- Helm chart templates are not rendered. Most do not parse as YAML and stay
  with the generic detector. In those that do, a container image written as a
  Go template action is skipped, because its value lives in the chart's values
  files.
- JSON manifests are not claimed.
- YAML aliases are not followed.

## Revisit criteria

- A second format needs content matching and the two-step order above does not
  fit it.
- Parsing manifests twice shows up as a measurable cost on large repositories.
