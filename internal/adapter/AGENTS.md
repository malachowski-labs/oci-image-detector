# AGENTS.md — adapters

Rules for the detectors and reporters in this directory. The repository-wide
rules in the root [`AGENTS.md`](../../AGENTS.md) apply as well.

## Detectors

The contract is defined by the doc comments in
[`pkg/port/detector.go`](../../pkg/port/detector.go). Read them first; the
rules below add what the interfaces cannot express.

### Structure

1. One package per file format. It exports `Strategy` and `Detector`,
   and registers itself from `init` with `registry.MustRegister` (or
   `MustRegisterDir`). Shipping it means adding one blank import to
   `cmd/detectors.go`.
2. A detector holds no state and performs no I/O of its own: it works on the
   bytes or the `fs.FS` it is handed.
3. `Name()` returns `string(Strategy)`. The value is permanent once released.
4. A detector never imports another detector.

### Claiming files

1. `Match` is cheap and looks at the path only.
2. Specialists must claim disjoint sets of files; order within a tier is not a
   precedence. When a new specialist's files overlap an existing one's, narrow
   one of them in the same change and add a `Match` test for the overlap.
3. Files under the directories GitHub reserves for workflows and actions belong
   to the GitHub Actions detector. Other YAML detectors decline them through
   `githubpath.IsReserved`.
4. A directory-aware detector names its files through `Owns`, by path alone,
   and `DetectDir` covers every owned file of a matching directory. The scan
   service keeps owned files away from file-level detectors, so no file-level
   detector needs to know about them.

### Finding images

1. Precision over recall. A detector reports a string only when the file's
   structure says it is an image (a `FROM` line, a service's `image` key), or
   when it passes `imageref.Candidates`. Never report on a looser pattern.
2. Build every `ImageRef` with `imageref.Parse`. Do not assemble or validate
   references by hand.
3. What cannot be resolved statically is reported raw and unparsed, or not at
   all. Never guess a value.
4. Honour the inline annotation with `suppress.IsIgnoredLine`, applied to the
   text of the line or comment the reference sits on. Do not match the
   annotation string yourself.
5. `Finding.FilePath` is the path the detector was given, unchanged.
   `Finding.Line` is the 1-based line the reference is written on, or 0 when
   the format gives no position.
6. On malformed input, return the findings gathered so far together with the
   error. Never panic on file content.
7. Parsing structured formats uses a real parser already in `go.mod`. Line or
   regex scanning is for formats without one; see `docs/adr/0001` before
   changing how Dockerfiles are read.

### Tests

Each detector has a `Match` table and a `Detect` table. The `Detect` table
covers at least: the plain case, each supported variant of the format, an
unresolvable reference, a suppressed line, a near-miss that must not be
reported, and empty and malformed input.

### Documentation

Adding a detector, or changing which files one claims or what it reports,
updates the README: the supported formats and the list of detector names. The
`--disable-detector` help text is derived from the registry and needs no edit.

## Reporters

1. A reporter formats findings and writes them to its destination. It does not
   filter, sort or deduplicate; findings arrive sorted.
2. It returns write errors to the caller. A failed report fails the command.
3. The JSON report is a public schema: adding a field is compatible, renaming
   or removing one is a breaking change.
