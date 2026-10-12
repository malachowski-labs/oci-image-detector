# AGENTS.md

Rules for AI assistants working in this repository. They are binding: when a
rule and a request conflict, stop and ask instead of picking one.

The detector-specific rules live in
[`internal/adapter/AGENTS.md`](internal/adapter/AGENTS.md) and apply on top of
these.

## What this project is

A Go CLI and GitHub Action that scans a directory tree for OCI image
references. The README describes behaviour and usage; `docs/adr/` records the
decisions that are not obvious from the code. Read the relevant ADR before
changing the area it covers.

## Architecture

| Package | Role | May import |
|---|---|---|
| `pkg/domain` | Value objects (`ImageRef`, `Finding`) | standard library only |
| `pkg/port` | Interfaces between the service and its adapters | `pkg/domain` |
| `pkg/imageref`, `pkg/suppress` | Helpers shared by detectors | `pkg/domain` |
| `pkg/registry` | Collects and orders detectors | `pkg/port` |
| `internal/service` | The scan use case | `pkg/domain`, `pkg/port` |
| `internal/adapter/*` | Detectors and reporters | `pkg/*`, shared `internal/*` helpers |
| `cmd` | Flags and wiring | everything |

1. Dependencies point inward. `pkg/` never imports `internal/`; the service
   never imports an adapter; one adapter never imports another.
2. Knowledge two adapters share goes into a small, side-effect-free package
   (see `internal/githubpath`), not into a copy in each adapter.
3. Do not add a package, layer, interface or option for a need that does not
   exist yet. An interface is justified by a second implementation or by a
   test seam that is actually used.

## Public API

Everything under `pkg/` and `cmd.Execute` is imported by third-party detector
authors (README, "Custom detectors").

1. Removing, renaming or changing the signature or semantics of an exported
   symbol is a breaking change. Do not make one without explicit approval, and
   call it out in the summary and the commit message (`feat!:` / `fix!:`).
2. Do not export a symbol because one caller would find it convenient.
3. Detector names and `Finding.Strategy` values, the JSON report schema, CLI
   flags, exit codes and the `oci-image-detector:ignore` annotation are
   contracts too. Changing any of them needs approval and a README update in
   the same change.

## Change discipline

1. One concern per change. A refactor does not alter behaviour; a behaviour
   change does not carry unrelated clean-up. If a task needs both, do them as
   separate, separately described steps.
2. Keep the diff minimal: touch only what the task requires, and match the
   surrounding naming, comment density and idiom.
3. Understand before changing. If the reason for existing code is unclear, ask;
   do not assume it is dead or wrong. Removing code requires showing it is
   unused or unreachable, not just that the tests still pass.
4. A bug found along the way is reported, not silently fixed, unless it is
   inside the lines the task already rewrites.
5. No new dependency (Go module, tool, GitHub Action) without discussing it
   first.
6. Never commit, push, tag or open a pull request without an explicit order.

## Code

1. Small functions with one job; the linter caps cyclomatic complexity at 15.
   Split by responsibility, not to satisfy the number.
2. Return values instead of filling out-parameters. Return errors instead of
   logging them, unless the scan must go on (a file that cannot be read or
   parsed must not stop the scan of its siblings).
3. Wrap errors with `%w` and context naming the operation and its subject.
   Compare with `errors.Is` / `errors.As`. Declare a sentinel error only when a
   caller branches on it.
4. Never `panic` outside package initialisation.
5. Prefer the standard library (`slices`, `maps`, `cmp`, `strings.Cut…`) over
   hand-written loops and helpers.
6. No package-level mutable state apart from the default detector registry,
   which is written only during `init`.
7. Scanned files are untrusted input. Anything that evaluates, expands or
   buffers them needs an explicit bound.
8. Output must be deterministic: never let map iteration order reach a result.

## Comments and documentation

1. Every package and every exported identifier has a doc comment. Unexported
   functions get one when their contract is not obvious from the signature.
2. Comments explain why and state contracts. They do not restate the code or
   narrate the change history.
3. Update comments, the README and ADRs in the same change that makes them
   stale. A decision that rejects an obvious alternative gets an ADR.
4. Keep documentation lean: describe purpose and structure, and point to where
   configuration lives rather than reproducing identifiers, hostnames or
   credential details inline.

## Tests

1. Every behaviour change comes with a test that fails without it. Every bug
   fix comes with a regression test.
2. Table-driven tests with named cases, in the external `_test` package, using
   only the standard `testing` package. An internal test package is acceptable
   only for unexported logic that cannot be reached through the public surface.
3. Tests use in-memory input (`fstest.MapFS`, byte slices, `t.TempDir()`); no
   network and no dependence on the working directory.
4. Do not weaken, skip or delete a test to make a change pass. If a test is
   wrong, say why before changing it.

## Verification

Run all of these before reporting a change as done, and report the actual
result, including failures:

```sh
go build ./...
go test -race -count=1 ./...
golangci-lint run ./...
go mod tidy   # must leave go.mod and go.sum unchanged
```

1. Formatting and linting are defined by `.golangci.yml`. Do not add `//nolint`
   without a reason on the same line, and do not relax the configuration to
   make a finding go away.
2. For a refactor, also show that the output is unchanged: scan the same tree
   with the binary built before and after, and compare stdout and the JSON
   report.

## Commits

Conventional Commits, enforced by `.conform.yaml`: lower-case imperative
header without a trailing period, at most 89 characters, using one of the
listed types.

## Stop and ask

- The task needs a breaking change, a new dependency or a new package.
- Two rules here conflict, or a rule conflicts with the request.
- The change would touch CI workflows, the release process or the action
  interface beyond what was asked.
- You found a bug or a design problem outside the scope of the task.
