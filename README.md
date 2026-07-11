# trace-check

A dialect-agnostic **requirement traceability checker**. It reconciles a
requirements catalog against the tests that claim to cover it, the waivers that
excuse the rest, and an optional per-requirement classification — then
regenerates a traceability matrix. When the four artifacts drift, it fails
loudly and names the requirement at fault.

It started life as a project-specific tool and was extracted to be reusable:
how requirement IDs are shaped, how the catalog markdown is laid out, what tag
keyword marks coverage, which languages to scan, and how the matrix is labelled
all come from a JSON config. With no config it uses built-in defaults.

## Install

```
go install github.com/fkautz/trace-check/cmd/trace-check@latest
```

Or use it as a library:

```go
import tracecheck "github.com/fkautz/trace-check"
```

## The model

trace-check reconciles four core artifacts (plus optional architecture/policy):

| Artifact | What it is |
|---|---|
| **Catalog** | every requirement as a stable ID, in markdown (optional meta fields) |
| **Tags** | tests declaring `<keyword> <ID>` in a comment (e.g. `// Verifies: REQ-001`) |
| **Waivers** | requirements excused from testing, each with an allowed reason |
| **Classification** | optional per-requirement axis (e.g. whether a requirement is observable by a black-box test) |
| **Architecture** | optional closed vocabulary of Components / Invariants for catalog field enums |
| **Policy rules** | optional when→coverage constraints driven by catalog meta |

It always enforces **integrity**:

- every tag and waiver names a real catalogued requirement;
- no requirement is both waived and tested;
- waiver reasons come from the configured allowed set;
- each requirement is classified at most once;
- a single test tags at most one requirement, so a failing test attributes to
  exactly one requirement by name;
- required catalog meta fields are present and match enums / architecture names;
- structured `Covers:` targets (when used) exist, are well-formed, and form no cycles.

With `-strict` it additionally enforces **full coverage** for requirements in
scope: each must be tagged or waived, and classification / policy rules that
demand a coverage class must be satisfied. Scope can be the whole catalog
(legacy) or filtered by **Phase** and/or keyword class (`must` / `should` / …)
via config `strict.*`, flags `-strict-phase` / `-strict-class`, or a named
`-profile`.

On a clean run it regenerates the matrix at `-out` (and optional `-out-json`).
`-check-output` compares those files byte-for-byte without rewriting them.
On any traceability problem matrix outputs remain untouched and the command
exits non-zero; `-problems-json` is the deliberate exception and records
deterministic diagnostics on both clean and failed checks.

### Architecture adherence

Declare extra catalog fields and optionally bind them to an architecture
registry:

```json
{
  "catalog": {
    "fields": [
      {"name": "Component", "required": true, "enumFrom": "architecture.components"},
      {"name": "Phase", "required": true, "enum": ["1", "2", "later"]},
      {"name": "Kind", "required": true, "enum": ["invariant", "encoding", "ops"]}
    ]
  },
  "architecture": {"path": "docs/architecture.md"},
  "strict": {
    "phases": ["1"],
    "keywordClasses": ["must"],
    "waiverReasonsSatisfy": ["covered-by", "documented-deviation"]
  },
  "policy": {
    "rules": [
      {"when": {"Kind": ["encoding"]}, "strictRequiresCoverageClass": "conformance"},
      {"when": {"Kind": ["ops"]}, "allowUncovered": true}
    ]
  },
  "profiles": {
    "phase1-freeze": {
      "strict": true,
      "strictPhases": ["1"],
      "strictKeywordClasses": ["must"]
    }
  }
}
```

Architecture file shape:

```markdown
### Components
- fault-in
- cas

### Invariants
- I-VERIFY — no exposure before verify
```

Waivers vs. policy rules: for backward compatibility, an omitted
`strict.waiverReasonsSatisfy` lets any valid waiver satisfy base `-strict`
coverage. Set it to the deliberate-excusal reasons a release gate accepts (or
to `[]` to accept none). A configured `covered-by` reason only counts when its
`Covers` target carries an actual tag. Separately, a
`strictRequiresCoverageClass` rule is satisfied only by reasons in
`policy.waiverReasonsSatisfy` — by default `covered-by` and
`documented-deviation` — and covered-by's target must carry that required
coverage class.

Structured covered-by (optional but recommended):

```markdown
### REQ-API-008
- Reason: covered-by
- Covers: REQ-API-001, REQ-API-002
- Rationale: union of 001 and 002.
```

`Covers` is a comma-separated list, so a composite may name every covering
requirement; each is checked (well-formed, in the catalog, not itself, no cycle).
Set `waivers.requireCoversForCoveredBy` to require the `Covers` line. Set
`waivers.coversForbidTargetReasons` (e.g. `["superseded"]`) to flag a `Covers`
target whose own waiver reason is in that list — a covered-by must point at an
active covering requirement, not one that was itself excused away (a retired /
superseded id, or a not-implemented placeholder).

### Multiple scopes

One dialect can validate several catalogs (e.g. a core and a server catalog).
Tag checking is filtered to the ID *series* present in the loaded catalog, so
one scope ignores another scope's tags. Run trace-check once per scope with
different `-catalog`/`-classification`/`-waivers`/`-out` flags.

## Tag syntax

A test declares coverage with a comment line whose keyword (default
`Verifies:`) is followed by exactly one requirement ID:

```go
// Verifies: REQ-CORE-042          // Go: on a Test*/Fuzz*/Example* function
```

```rust
/// Verifies: RUST-007            // Rust: in the doc comment above #[test]
#[test]
fn accepts_valid() { /* ... */ }
```

Go tests are discovered by AST (functions only — a test-shaped *method* is
ignored). Other languages are scanned by comment: a contiguous comment block is
attributed to the next test item, where "test item" is armed by a marker such
as `#[test]` or by a configured name prefix. A stray tag separated from the
test by a blank line or unrelated code is not misattributed.

## Configuration

With no `-config`, the built-in defaults match a `REQ-<SERIES>-NNN` /
`Verifies:` / markdown dialect (Go collector). A config file **overlays** the
defaults — it names only the fields it changes. Scalar fields merge over the
defaults; slice fields are replaced when present and kept when omitted, so an
explicit empty array (`"subtypes": []`) *removes* a default.

```jsonc
{
  "idGrammar": {
    "pattern": "REQ-[A-Z][A-Z0-9]*-(?:IMP-|DEC-)?\\d{3}",  // inner ID regex (no anchors)
    "headingPrefix": "REQ-",                            // for malformed-heading detection
    "headingCandidatePattern": "",                     // regex alternative for multi-series IDs
    "seriesPattern": "^REQ-([A-Za-z0-9]+)-",            // group 1 = scope series
    "subtypes": [{"marker": "-IMP-", "class": "implicit"}]  // IDs with no Keyword line
  },
  "catalog": {"keywordField": "Keyword", "sectionField": "Section",
              "sectionRefPattern": "§[\\d.]*\\d"},
  "keywordClasses": [
    {"class": "must",   "keywords": ["MUST", "SHALL", "REQUIRED"]},
    {"class": "should", "keywords": ["SHOULD", "RECOMMENDED"]},
    {"class": "may",    "keywords": ["MAY", "OPTIONAL"]}
  ],
  "tag": {
    "keyword": "Verifies:",
    "commentMarkers": ["//", "///", "/*", "*/", "*"],
    "collectors": [
      {"lang": "go", "funcPrefixes": ["Test", "Fuzz", "Example"]},
      {"lang": "comment", "fileSuffix": ".rs",
       "testMarkers": ["#[test]", "#[tokio::test]"],
       "namePattern": "fn\\s+([A-Za-z0-9_]+)"}
    ]
  },
  "coverage": {
    "default": "unit",
    "rules": [{"class": "blackbox",
               "pathPrefixes": ["compliance/"],
               "funcPrefixes": ["TestScenario", "TestSmoke"]}]
  },
  "waivers": {"reasonField": "Reason", "rationaleField": "Rationale",
              "reasons": ["covered-by", "not-implemented", "documented-deviation"]},
  "classification": {
    "classField": "Class", "reasonField": "Reason",
    "values": [
      {"name": "wire-observable", "strictRequiresCoverageClass": "blackbox"},
      {"name": "not-observable", "requiresReason": true, "forbidsCoverageClass": "blackbox"}
    ]
  },
  "matrix": {"primaryLabel": "Unit coverage",
             "secondaryClass": "blackbox", "secondaryLabel": "Black-box coverage",
             "generatedBy": "`trace-check`"},
  "skipDirs": [".git", "testdata", "docs"]
}
```

`headingCandidatePattern` is matched against the heading text after `### `;
leading anchors therefore work as expected. When set, it takes precedence over
`headingPrefix`. Choose it broadly enough to catch malformed IDs while excluding
any narrative H3 headings used by the artifact format.

Configuration validation rejects unknown policy selector fields, closed-set
selector values, coverage classes, strict/profile filters, contradictory policy
effects, and matrix grouping fields. Architecture-backed selector values are
validated when the registry is loaded by a check.

### Collectors

- **`go`** — scans `*_test.go` via the Go AST; `funcPrefixes` names test
  functions.
- **`comment`** — language-agnostic. `fileSuffix` selects files; a contiguous
  comment block is attributed to the next test item, armed by any of
  `testMarkers` (line prefixes like `#[test]`) or by a name in `funcPrefixes`.
  `namePattern`'s first capture group names the item (default matches
  `fn`/`func`).

### Coverage classes

`coverage.rules` assign a class to a tag by file path prefix and/or test-name
prefix (first match wins, else `coverage.default`). The matrix shows two
columns — the **primary** class and the configured **secondary** class — and
`classification` rules reference these class names.

## CLI

```
trace-check                 # default dialect; integrity + regenerate matrix
trace-check -strict         # release gate: full coverage required
trace-check -config tracecheck.json -strict     # custom dialect
trace-check -config tracecheck.json -check-output  # generated-file drift gate
trace-check -version                            # available build provenance

# a second scope sharing the dialect:
trace-check -catalog server/spec/requirements.md \
            -classification server/spec/classification.md \
            -waivers server/spec/waivers.md \
            -out docs/traceability-server.md
```

| Flag | Meaning |
|---|---|
| `-config` | JSON dialect config; empty uses built-in defaults |
| `-root` | repository root scanned for tags (default `.`) |
| `-catalog` | catalog path, relative to `-root` |
| `-classification` | classification path; absent file is dormant |
| `-waivers` | waivers path; absent file means none |
| `-out` | matrix output path; empty disables generation |
| `-out-json` | machine-readable matrix output path |
| `-problems-json` | deterministic diagnostic report, written even when the check fails |
| `-check-output` | compare matrix outputs without writing |
| `-strict` | enforce the full-coverage policy |
| `-strict-phase` / `-strict-class` | override strict scope filters |
| `-profile` | apply a named strictness/filter profile |
| `-version` | print module/build version and available VCS provenance |

Exit status: `0` clean, `1` traceability or output I/O problems, `2` bad usage /
invalid config. Reconciliation findings prevent matrix generation; an I/O
failure on a later file in a multi-output generation can leave an earlier file
updated, so CI drift gates should use the non-writing `-check-output` mode.

### Problem reports and ratchets

`-problems-json` emits schema version 1 with the selected profile, summary
counts, effective strict scope, config digest, CLI build metadata, and
named control-artifact paths/content digests from the exact bytes reconciled,
sorted successful tag evidence with its digest, output-verification scope, and
problems sorted and deduplicated by stable key. Coverage evidence records tags
rather than hashing every source-tree byte. Reports carry `complete: false`
until reconciliation finishes, so fatal input failures replace rather than
preserve a stale baseline. Only strict backlog codes are marked
`baselinable`: `coverage-required`, `coverage-class-required`,
`waiver-reason-not-accepted`, and `waiver-proxy-not-covered`. Parser,
configuration, catalog, waiver, classification, and tag problems use the
`integrity` code and must never be accepted into a backlog baseline.

When `-check-output` is requested, expected baselinable strict-backlog findings
do not skip the byte comparison. If an integrity finding makes safe matrix
rendering impossible, the report adds a non-baselinable “verification skipped”
problem instead of claiming the output gate ran.

## Full reference

[`SKILL.md`](SKILL.md) is the complete operating manual — artifact file
formats, every config field, common workflows, and a problem→fix table. It is
byte-for-byte identical to `trace-check -help` and generated from it, so it
never drifts (a test fails if it does; regenerate with
`go test ./cmd/trace-check -run TestSkillDocInSync -update`). It is written to
be usable as an LLM skill without running the binary.

A complete worked example in a non-Go dialect lives in
[`testdata/rust-project`](testdata/rust-project).

## License

MIT — see [LICENSE](LICENSE).
