# trace-check — operating reference

`trace-check` is a dialect-agnostic requirement-traceability checker. This
document is the complete operating manual — byte-for-byte identical to the
output of `trace-check -help` — so it can be read as a skill without running
the binary.

GENERATED FILE — do not edit by hand. Refresh it after changing the CLI usage
text with:

    go test ./cmd/trace-check -run TestSkillDocInSync -update

```text
trace-check — requirement traceability checker

OVERVIEW
  trace-check proves that every requirement a project states is either tested
  or deliberately waived, and that an optional per-requirement classification
  is honored. It reconciles four artifacts, FAILS (non-zero, naming the
  requirement) when they drift, and on a clean run regenerates a traceability
  matrix. It is language- and dialect-agnostic: a JSON config (-config)
  describes the ID shape, file formats, tag keyword, and languages. With no
  config it uses built-in defaults: the "REQ-<SERIES>-NNN" / "Verifies:"
  dialect over Go tests.

  Two everyday uses: a CI / pre-commit GATE (`trace-check -strict`), and a
  matrix GENERATOR (writes -out on success). Invoke with flags only; there are
  no subcommands except `help`.

CONCEPTS
  Requirement     a stated obligation with a stable ID (e.g. REQ-API-001),
                  listed in the CATALOG.
  Series          the ID segment used to scope a run (CORE in REQ-CORE-001). Tags
                  for series absent from the loaded catalog are ignored, so
                  several catalogs can live in one tree and be checked
                  independently (run once per scope; see -catalog).
  Tag             a comment in a test naming the ONE requirement it verifies.
  Waiver          a catalog requirement excused from testing, with an allowed
                  reason and a rationale. covered-by waivers MAY name a Covers
                  target ID (structured covered-by).
  Classification  an optional second axis per requirement (e.g. wire-observable
                  vs not-observable). A value may require a reason, forbid a
                  coverage class, or (under -strict) require one.
  Coverage class  a label on each tag (default "unit"); rules promote some tags
                  to another class (e.g. "blackbox") by file-path / test-name
                  prefix. The matrix has two columns by default (primary and
                  secondary); matrix.coverageColumns enables N columns.
  Catalog meta    optional fields (Component, Phase, Kind, …) declared in
                  catalog.fields; validated for required/enum/enumFrom.
  Architecture    optional closed vocabulary of Components and Invariants used
                  as enumFrom targets for catalog fields.
  Policy rules    when→coverage constraints driven by catalog meta / KeywordClass.
  Strict filters  -strict can be limited to Phase values and/or keyword classes
                  (config strict.* , flags -strict-phase/-strict-class, or a
                  named -profile).

ARTIFACT FILE FORMATS (default dialect; the field labels are configurable)
  All three are markdown; entries are H3 headings ("### "). Exact line forms:

  CATALOG (default spec/requirements.md) — one block per requirement:
      ### REQ-API-001 — Short title       (the " — title" suffix is optional)
      - Section: §4.2                     (free text; a "§x.y" ref is extracted)
      - Keyword: MUST | Actor: Server     (text after " | " is ignored)
      - Component: fault-in               (optional; when catalog.fields configured)
      - Phase: 1
      - Text: "..."                       (any other lines are ignored)
    Keyword sets the policy class: MUST/SHALL/REQUIRED->must,
    SHOULD/RECOMMENDED->should, MAY/OPTIONAL->may. "Subtype" IDs carrying a
    configured marker (default -IMP-, -DEC-) need NO Keyword line and take their
    class from the marker (implicit, decision).

  WAIVERS (default spec/waivers.md) — heading is the BARE ID, no title:
      ### REQ-API-007
      - Reason: not-implemented           (must be one of the allowed reasons)
      - Rationale: planned for v2; see #123.
      ### REQ-API-008
      - Reason: covered-by
      - Covers: REQ-API-001               (optional structured target; required if
                                          waivers.requireCoversForCoveredBy)
      - Rationale: special case of 001.
    Default allowed reasons: covered-by, not-implemented, documented-deviation,
    deployment-guidance, foundational. A requirement is EITHER tested OR waived,
    never both.

  CLASSIFICATION (default spec/classification.md; optional — absent = OFF):
      ### REQ-API-001
      - Class: wire-observable
      ### REQ-API-007
      - Class: not-observable
      - Reason: internal-only behavior.    (required iff that value demands it)
    When the file exists, every catalog requirement must appear exactly once.

  ARCHITECTURE (optional; config architecture.path or -architecture):
      ### Components
      - fault-in
      - cas
      ### Invariants
      - I-VERIFY — no exposure before verify
    Catalog fields with enumFrom architecture.components / architecture.invariants
    must use these names.

  Catalog headings MAY carry " — title"; waiver and classification headings
  must be the bare ID. A "### <headingPrefix>..." line whose ID is malformed is
  REPORTED, never silently skipped.

TAG SYNTAX
  In a test, start a comment with the keyword (default "Verifies:") then exactly
  ONE requirement ID:

      // Verifies: REQ-API-042         Go: in a Test*/Fuzz*/Example* doc comment
      /// Verifies: RUST-007           comment-scanned languages: in the test's
      #[test]                          own comment, contiguous with the item
      fn accepts() { /* ... */ }

  Rules:
    - ONE ID per test (a test tagging two IDs is an error — split it, or use a
      covered-by waiver). This keeps a failing test attributable to one req.
    - Go: only Test/Fuzz/Example FUNCTIONS are scanned (methods are ignored).
    - Comment-scanned languages: a contiguous comment block attaches to the
      next test item; an item counts as a test when armed by a marker (e.g.
      #[test]) or a configured name prefix. A blank line or unrelated code
      between the comment and the item breaks attribution, so a stray
      "Verifies:" elsewhere is not miscounted.
    - Files under skipDirs (default .git, testdata, docs) are never scanned.

FLAGS
  -config FILE          JSON dialect config; empty uses built-in defaults
  -root DIR             repository root scanned for tags (default ".")
  -catalog PATH         catalog path, relative to -root
  -classification PATH  classification path, relative to -root (absent: dormant)
  -waivers PATH         waivers path, relative to -root (absent: none)
  -architecture PATH    architecture registry, relative to -root (overrides config)
  -out PATH             matrix output, relative to -root; empty disables
  -out-json PATH        machine-readable matrix JSON; empty disables
  -problems-json PATH   deterministic diagnostics JSON, written even on failure
  -check-output         compare -out/-out-json with generated bytes; never write
  -strict               enforce full-coverage policy (release gate)
  -strict-phase LIST    comma-separated Phase filter under -strict (e.g. 1,2)
  -strict-class LIST    comma-separated keyword classes under -strict (e.g. must)
  -profile NAME         apply config profiles[NAME] (may set strict + filters)
  -version              print module/build version and available VCS provenance
  -help                 show this message ("help" and --help also work)

PROBLEMS JSON
  -problems-json writes a deterministic schemaVersion 1 report after artifact
  reconciliation, including when traceability problems make the command fail.
  Problems are sorted by stable key. Only release-backlog codes are marked
  baselinable: coverage-required, coverage-class-required,
  waiver-reason-not-accepted, and waiver-proxy-not-covered. All parser,
  configuration, catalog, tag, waiver, and classification drift is emitted as
  non-baselinable integrity evidence. Fatal usage or input I/O errors can occur
  before a report exists. The report also records the effective strict scope,
  selected profile, config digest, CLI build metadata, and named
  control-artifact paths/content digests from the exact bytes reconciled,
  sorted successful tag evidence with its digest, and output-verification
  scope. Coverage evidence records tags rather than hashing every source-tree
  byte. Equivalent duplicate keys collapse to one obligation. A safe output
  path first receives complete:false and is replaced with complete:true only
  after reconciliation finishes.

  Source-checkout builds normally expose a full revision and modified state.
  Module-cache builds may report those fields as unknown unless the build
  injects main.buildRevision/main.buildModified with -ldflags.

OUTPUT VERIFICATION
  -check-output compares the configured -out and/or -out-json files byte for
  byte with freshly rendered output. It never creates or rewrites them. A
  missing or stale output fails and names the path. Baselinable strict backlog
  findings do not skip this comparison; an integrity blocker produces an
  explicit non-baselinable verification-skipped problem.

CONFIG (-config FILE, JSON)
  Overlay semantics: the file is layered over the built-in defaults, so name
  ONLY what you change. Scalar fields (including inside nested objects) MERGE
  over the defaults; a slice present in the JSON fully REPLACES the default
  slice; an omitted slice KEEPS the default. An explicit empty array therefore
  REMOVES a default (e.g. "subtypes": [] drops the IMP/DEC subtypes). Unknown
  fields and bad regexes are rejected at load (exit 2).

  Field reference:
    idGrammar.pattern          inner ID regex, no anchors (must match a whole ID)
    idGrammar.headingPrefix    literal ID start, for malformed-heading detection
    idGrammar.headingCandidatePattern  regex matched at the start of any ID-like
                               heading text (leading ^ is supported); overrides
                               headingPrefix for multi-series catalogs
    idGrammar.seriesPattern    regex; capture group 1 = the scope series
    idGrammar.subtypes[]       {marker,class}: IDs containing marker need no Keyword
    catalog.keywordField       catalog label for the policy keyword ("Keyword")
    catalog.sectionField       catalog label for the section ("Section")
    catalog.sectionRefPattern  regex to canonicalize the section ref ("" = raw text)
    catalog.fields[]           {name,required,enum[],enumFrom}: extra meta fields
                               enumFrom: architecture.components|architecture.invariants
    keywordClasses[]           {class,keywords[]}: ordered; first contained keyword wins
    tag.keyword                the tag marker ("Verifies:")
    tag.commentMarkers[]       comment prefixes stripped before the keyword
    tag.collectors[]           {lang:"go",funcPrefixes[]} and/or {lang:"comment",
                               fileSuffix,testMarkers[],funcPrefixes[],namePattern}
    coverage.default           class for tags matching no rule ("unit")
    coverage.rules[]           {class,pathPrefixes[],funcPrefixes[]}: first match wins
    waivers.reasonField / rationaleField / reasons[]
    waivers.coversField        structured covered-by target label ("Covers")
    waivers.coveredByReason    reason value for covered-by ("covered-by")
    waivers.requireCoversForCoveredBy  require Covers line for covered-by
    classification.classField / reasonField / values[]
                               values[]: {name, requiresReason, forbidsCoverageClass,
                               strictRequiresCoverageClass}
    architecture.path          registry markdown path (relative to -root)
    architecture.componentSection / invariantSection  heading titles
    policy.rules[]             {when, strictRequiresCoverageClass,
                               forbidsCoverageClass, allowUncovered}
                               when keys: catalog meta fields or "KeywordClass"
    policy.waiverReasonsSatisfy[]  waiver reasons that satisfy a
                               strictRequiresCoverageClass rule (default
                               covered-by, documented-deviation; [] = none).
                               covered-by only counts when its Covers target
                               carries a tag of the required coverage class
    strict.phases[]            Phase meta values in scope under -strict
    strict.keywordClasses[]    policy classes in scope under -strict (e.g. must)
    strict.phaseField          meta field for phase (default "Phase")
    strict.waiverReasonsSatisfy[]  waiver reasons accepted for base strict coverage;
                               omitted = any valid reason (legacy), [] = none;
                               covered-by also needs a tagged Covers target
    profiles                   map of name -> {strict, strictPhases, strictKeywordClasses}
    matrix.primaryClass/primaryLabel / secondaryClass/secondaryLabel /
           bothLabel/primaryOnlyLabel/secondaryOnlyLabel / generatedBy
    matrix.coverageColumns[]   {class,label}: N-column mode (replaces two-column)
    matrix.groupBy             catalog meta field to section the matrix
    skipDirs[]                 directory names never scanned

  Example (architecture-aware dialect, abbreviated):

    {
      "catalog": {
        "fields": [
          {"name": "Component", "required": true, "enumFrom": "architecture.components"},
          {"name": "Phase", "required": true, "enum": ["1", "2", "later"]},
          {"name": "Kind", "required": true, "enum": ["invariant", "encoding", "ops"]}
        ]
      },
      "architecture": {"path": "docs/architecture.md"},
      "waivers": {"requireCoversForCoveredBy": true},
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
      "coverage": {
        "default": "unit",
        "rules": [
          {"class": "conformance", "pathPrefixes": ["conformance/"]},
          {"class": "integration", "pathPrefixes": ["benchmarking/"]}
        ]
      },
      "matrix": {
        "coverageColumns": [
          {"class": "unit", "label": "Unit"},
          {"class": "conformance", "label": "Conformance"},
          {"class": "integration", "label": "Integration"}
        ],
        "groupBy": "Component"
      },
      "profiles": {
        "phase1-freeze": {"strict": true, "strictPhases": ["1"], "strictKeywordClasses": ["must"]}
      }
    }

TYPICAL TASKS
  Add a requirement     add a "### <ID>" block to the catalog (with a Keyword
                        line and any required meta fields), then EITHER tag a
                        test OR add a waiver; under -strict it must be one or
                        the other (subject to phase/class filters). If a
                        classification file exists, add a "### <ID>" entry there
                        too.
  Cover a requirement   add "// Verifies: <ID>" to the relevant test's comment.
  Waive a requirement   add a "### <ID>" block to the waivers file with an
                        allowed Reason and a Rationale (and remove any test tag).
                        For covered-by, prefer "- Covers: <target-ID>".
  Phase-1 freeze        trace-check -strict -strict-phase 1 -strict-class must
                        (or -profile phase1-freeze).
  Add a scope           run again with -catalog/-classification/-waivers/-out
                        pointing at the second set; reuse the same -config.
  Support a language    add a {"lang":"comment", ...} entry to tag.collectors.

PROBLEMS AND FIXES (each is printed as "  PROBLEM: <id>: <message>")
  malformed requirement heading             fix the ID in the "### " line
  duplicate requirement ID                  remove or rename the duplicate catalog block
  missing/unclassifiable Keyword line       add "- Keyword: MUST|SHOULD|MAY"
                                            (or make it a subtype ID)
  missing required catalog field X          add "- X: ..." under the requirement
  invalid X "value"                         use an allowed enum / architecture name
  tagged by ... but not in the catalog      add the requirement, or fix the tag
  malformed tag line                        one well-formed ID after the keyword
  tags N requirements; one per test         split the test (or covered-by waiver)
  invalid waiver reason                     use an allowed reason
  waiver has no Rationale line              add "- Rationale: ..."
  covered-by waiver has no Covers line      add "- Covers: <ID>" (when required)
  Covers target not in the catalog          fix the target ID
  covered-by cycle                          break the Covers cycle
  has both a waiver and tagged tests        remove the waiver OR the test tag
  duplicate waiver / classification         remove the duplicate block
  waived/classified but not in the catalog  add it to the catalog (or remove)
  no tagged test or waiver        (-strict) tag a test or add a waiver
  waiver reason does not satisfy strict       tag a test or use an allowed,
                                              deliberate waiver reason
  covered-by target has no tagged test        tag the Covers target
  not classified            (file present)  add a classification entry
  <value> classification has no Reason      add "- Reason: ..."
  classified X but has a Y tag (stale)      reclassify, or remove the Y-class tag
  <value> but has no <class> coverage (-strict)
                                            add a test of that coverage class
  policy requires <class> coverage          add a tag of that coverage class, or
                                            waive with a deliberate-excusal reason
                                            (policy.waiverReasonsSatisfy)
  policy forbids <class> coverage           remove the forbidden-class tag

EXAMPLES
  # Default Go dialect, integrity check + regenerate the matrix:
  trace-check

  # Release gate (full coverage required):
  trace-check -strict

  # Phase-1 MUST freeze:
  trace-check -strict -strict-phase 1 -strict-class must

  # Named profile + JSON matrix:
  trace-check -config tracecheck.json -profile phase1-freeze \
              -out docs/traceability.md -out-json docs/traceability.json

  # A second scope sharing the same dialect:
  trace-check -catalog server/spec/requirements.md \
              -classification server/spec/classification.md \
              -waivers server/spec/waivers.md \
              -out docs/traceability-server.md

  # A Rust project with its own dialect config:
  trace-check -config tracecheck.json -strict

EXIT STATUS
  0  clean (requested outputs generated or verified)
  1  traceability or output I/O problem; reconciliation findings prevent
     generation, but a later I/O failure across multiple outputs may be partial
  2  bad usage or invalid config
```
