# 1. Dockerfile parsing: reject the buildkit AST, keep a zero-dependency parser

- Status: Accepted
- Date: 2026-06-12
- Deciders: project maintainers
- Related: [#24](https://github.com/malachowski-labs/oci-image-detector/issues/24), [#25](https://github.com/malachowski-labs/oci-image-detector/issues/25), [#26](https://github.com/malachowski-labs/oci-image-detector/issues/26)

## Context

Issue #26 proposes moving the specialist detectors to "Trivy-style" structured
parsing. Now that the generic detector is strict (#24) and only matches
host-qualified references with a tag or digest, the specialist detectors must
reliably catch the short forms (`nginx:1.25`, `org/repo:tag`, stage-aliased
`FROM` bases) in their own formats.

For Dockerfiles, the canonical structured approach — and what Trivy itself uses —
is the BuildKit Dockerfile parser:
`github.com/moby/buildkit/frontend/dockerfile/{parser,instructions}`. It returns
a typed AST (stages, instructions, base names, locations), which makes several
things that are awkward in a line scanner trivial:

- line continuations (`\`) and heredocs,
- global `ARG` values expanded into `FROM base:${VERSION}`,
- **stage-alias suppression** — not reporting `FROM builder` where `builder` is a
  previously declared stage,
- `COPY --from=<image>` references,
- parser directives (`# syntax=...`).

The question is whether the accuracy gain justifies adding BuildKit as a
dependency.

## Spike

We measured the footprint of importing only the two parser subpackages
(`parser` + `instructions`) into this project and using them realistically
(parse → typed stages → base names). Baseline is the project at the time of
writing.

| Metric                          | Baseline | With BuildKit | Delta            |
| ------------------------------- | -------- | ------------- | ---------------- |
| Binary size (`go build`, linux) | 7.2 MB   | 9.8 MB        | **+2.6 MB (+36%)** |
| Modules in graph (`go list -m all`) | 49   | 250           | **+201 (~5×)**   |

The 201 net-new modules are the entire container-runtime ecosystem —
`containerd/*` (snapshotters, cgroups, ttrpc, continuity), `moby/*`,
`docker/*`, gRPC, multiple protobuf runtimes, seccomp profiles, and so on — none
of which has anything to do with parsing the text of a Dockerfile. The parser is
a small corner of a very large module, so the module-level requirement pulls the
whole graph regardless of linker dead-code elimination.

## Decision

**Reject the BuildKit dependency for Dockerfile parsing.** Keep and enhance the
existing zero-dependency line parser in
`internal/adapter/dockerfile/detector.go`.

The line parser will be extended to cover the high-value cases that motivated
the AST, all of which are achievable without a parser library:

- join line continuations (`\`) before instruction parsing;
- track stage names declared via `FROM <img> AS <name>` and **skip** `FROM`
  bases that reference a known stage name;
- resolve global `ARG`/`ENV`-style defaults into `FROM` where they appear before
  the first stage; keep unresolved templates (`$VAR`, `${VAR}`) as raw findings;
- recognise `COPY --from=<image>` references that point at an external image
  (not a stage name or a build context).

Heredocs and parser directives are explicitly out of scope for now; they do not
carry base-image references in practice.

## Consequences

Positive:

- The supply-chain surface stays small (49 modules), consistent with the
  project's existing hardening posture (Trivy `DS-0026`, Renovate
  `minimumReleaseAge`). Fewer modules means less audit surface, less dependency
  churn, faster builds, and a smaller binary in the published image.
- No runtime ties to the container ecosystem for a static text scanner.

Negative / accepted limitations:

- The line parser is hand-maintained and will not be a 100% faithful Dockerfile
  frontend. Exotic constructs (heredocs carrying images, unusual directive
  placement) may be missed. This is acceptable: the detector's job is to find
  base-image references, not to build images.
- Stage-alias suppression and `ARG` expansion are re-implemented rather than
  inherited from a reference implementation, so they need their own tests.

## Revisit criteria

Reopen this decision if **either** holds:

- a concrete, real-world Dockerfile pattern is shown to be unparseable by the
  line parser and matters to users; **or**
- a lightweight, well-maintained Go Dockerfile AST library appears that does not
  drag in the container-runtime module graph.

## Related decisions (by principle, not measured by this spike)

These follow the same "structured parsing is good; heavy dependencies are not"
principle and are recorded here for context. They are not the subject of the
spike above and can be revisited independently:

- **Kubernetes / Helm / Compose**: parse with the already-present
  `gopkg.in/yaml.v3`. Do **not** add `k8s.io/api` or the Helm SDK, and do **not**
  render Helm templates. Node-walking keyed on `apiVersion`/`kind` and known
  container `image:` paths is sufficient and adds zero dependencies.
- **Reference validation**: keep `github.com/google/go-containerregistry`
  (already used by `internal/adapter/imageref`). Do **not** add
  `github.com/distribution/reference` — it would be a second grammar for a job
  already covered.
- **Terraform (HCL)**: the only remaining specialist whose accuracy genuinely
  benefits from a parser (`github.com/hashicorp/hcl/v2`). Deferred; the payoff is
  the least certain and the dependency is not free. Decide separately if/when
  Terraform precision becomes a priority.
