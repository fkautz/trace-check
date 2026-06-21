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
                  reason and a rationale.
  Classification  an optional second axis per requirement (e.g. wire-observable
                  vs not-observable). A value may require a reason, forbid a
                  coverage class, or (under -strict) require one.
  Coverage class  a label on each tag (default "unit"); rules promote some tags
                  to another class (e.g. "blackbox") by file-path / test-name
                  prefix. The matrix has two columns: primary and secondary
                  class. Classification rules reference these class names.

ARTIFACT FILE FORMATS (default dialect; the field labels are configurable)
  All three are markdown; entries are H3 headings ("### "). Exact line forms:

  CATALOG (default spec/requirements.md) — one block per requirement:
      ### REQ-API-001 — Short title       (the " — title" suffix is optional)
      - Section: §4.2                     (free text; a "§x.y" ref is extracted)
      - Keyword: MUST | Actor: Server     (text after " | " is ignored)
      - Text: "..."                       (any other lines are ignored)
    Keyword sets the policy class: MUST/SHALL/REQUIRED->must,
    SHOULD/RECOMMENDED->should, MAY/OPTIONAL->may. "Subtype" IDs carrying a
    configured marker (default -IMP-, -DEC-) need NO Keyword line and take their
    class from the marker (implicit, decision).

  WAIVERS (default spec/waivers.md) — heading is the BARE ID, no title:
      ### REQ-API-007
      - Reason: not-implemented           (must be one of the allowed reasons)
      - Rationale: planned for v2; see #123.
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
  -out PATH             matrix output, relative to -root; empty disables
  -strict               enforce full-coverage policy (release gate)
  -help                 show this message ("help" and --help also work)

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
    idGrammar.seriesPattern    regex; capture group 1 = the scope series
    idGrammar.subtypes[]       {marker,class}: IDs containing marker need no Keyword
    catalog.keywordField       catalog label for the policy keyword ("Keyword")
    catalog.sectionField       catalog label for the section ("Section")
    catalog.sectionRefPattern  regex to canonicalize the section ref ("" = raw text)
    keywordClasses[]           {class,keywords[]}: ordered; first contained keyword wins
    tag.keyword                the tag marker ("Verifies:")
    tag.commentMarkers[]       comment prefixes stripped before the keyword
    tag.collectors[]           {lang:"go",funcPrefixes[]} and/or {lang:"comment",
                               fileSuffix,testMarkers[],funcPrefixes[],namePattern}
    coverage.default           class for tags matching no rule ("unit")
    coverage.rules[]           {class,pathPrefixes[],funcPrefixes[]}: first match wins
    waivers.reasonField / rationaleField / reasons[]
    classification.classField / reasonField / values[]
                               values[]: {name, requiresReason, forbidsCoverageClass,
                               strictRequiresCoverageClass}
    matrix.primaryClass/primaryLabel / secondaryClass/secondaryLabel /
           bothLabel/primaryOnlyLabel/secondaryOnlyLabel / generatedBy
    skipDirs[]                 directory names never scanned

  Example (the built-in defaults, abbreviated — copy and trim to your dialect):

    {
      "idGrammar": {
        "pattern": "REQ-[A-Z][A-Z0-9]*-(?:IMP-|DEC-)?\\d{3}",
        "headingPrefix": "REQ-",
        "seriesPattern": "^REQ-([A-Za-z0-9]+)-",
        "subtypes": [{"marker": "-IMP-", "class": "implicit"}]
      },
      "catalog":  {"keywordField": "Keyword", "sectionField": "Section",
                   "sectionRefPattern": "§[\\d.]*\\d"},
      "keywordClasses": [{"class": "must", "keywords": ["MUST","SHALL","REQUIRED"]}],
      "tag": {
        "keyword": "Verifies:",
        "commentMarkers": ["//","///","/*","*/","*"],
        "collectors": [
          {"lang": "go", "funcPrefixes": ["Test","Fuzz","Example"]},
          {"lang": "comment", "fileSuffix": ".rs",
           "testMarkers": ["#[test]","#[tokio::test]"],
           "namePattern": "fn\\s+([A-Za-z0-9_]+)"}
        ]
      },
      "coverage": {"default": "unit",
                   "rules": [{"class": "blackbox",
                              "pathPrefixes": ["compliance/"],
                              "funcPrefixes": ["TestScenario","TestSmoke"]}]},
      "waivers": {"reasonField": "Reason", "rationaleField": "Rationale",
                  "reasons": ["covered-by","not-implemented"]},
      "classification": {"classField": "Class", "reasonField": "Reason",
        "values": [
          {"name": "wire-observable", "strictRequiresCoverageClass": "blackbox"},
          {"name": "not-observable", "requiresReason": true,
           "forbidsCoverageClass": "blackbox"}
        ]},
      "matrix": {"primaryLabel": "Unit coverage",
                 "secondaryClass": "blackbox", "secondaryLabel": "Black-box coverage",
                 "generatedBy": "`trace-check`"},
      "skipDirs": [".git","testdata","docs"]
    }

TYPICAL TASKS
  Add a requirement     add a "### <ID>" block to the catalog (with a Keyword
                        line), then EITHER tag a test OR add a waiver; under
                        -strict it must be one or the other. If a classification
                        file exists, add a "### <ID>" entry there too.
  Cover a requirement   add "// Verifies: <ID>" to the relevant test's comment.
  Waive a requirement   add a "### <ID>" block to the waivers file with an
                        allowed Reason and a Rationale (and remove any test tag).
  Add a scope           run again with -catalog/-classification/-waivers/-out
                        pointing at the second set; reuse the same -config.
  Support a language    add a {"lang":"comment", ...} entry to tag.collectors.

PROBLEMS AND FIXES (each is printed as "  PROBLEM: <id>: <message>")
  malformed requirement heading             fix the ID in the "### " line
  missing/unclassifiable Keyword line       add "- Keyword: MUST|SHOULD|MAY"
                                            (or make it a subtype ID)
  tagged by ... but not in the catalog      add the requirement, or fix the tag
  malformed tag line                        one well-formed ID after the keyword
  tags N requirements; one per test         split the test (or covered-by waiver)
  invalid waiver reason                     use an allowed reason
  waiver has no Rationale line              add "- Rationale: ..."
  has both a waiver and tagged tests        remove the waiver OR the test tag
  duplicate waiver / classification         remove the duplicate block
  waived/classified but not in the catalog  add it to the catalog (or remove)
  no tagged test or waiver        (-strict) tag a test or add a waiver
  not classified            (file present)  add a classification entry
  <value> classification has no Reason      add "- Reason: ..."
  classified X but has a Y tag (stale)      reclassify, or remove the Y-class tag
  <value> but has no <class> coverage (-strict)
                                            add a test of that coverage class

EXAMPLES
  # Default Go dialect, integrity check + regenerate the matrix:
  trace-check

  # Release gate (full coverage required):
  trace-check -strict

  # A second scope sharing the same dialect:
  trace-check -catalog server/spec/requirements.md \
              -classification server/spec/classification.md \
              -waivers server/spec/waivers.md \
              -out docs/traceability-server.md

  # A Rust project with its own dialect config:
  trace-check -config tracecheck.json -strict

EXIT STATUS
  0  clean (matrix regenerated when -out is set)
  1  one or more traceability problems (matrix not written)
  2  bad usage or invalid config
```
