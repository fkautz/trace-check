package tracecheck

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests are ported from the original tools/trace-check suite,
// adapted to the config-driven API. They run under Default() (the built-in
// dialect), so they double as the parity guarantee that extraction preserved
// behaviour.

const fixtureCatalog = `# Catalog

### REQ-CORE-001 — First rule
- Section: §4 (line 259); restated in §11 (line 771)
- Keyword: MUST | Actor: Deployment
- Text: "..."

### REQ-CORE-002 — Second rule
- Section: §7 (lines 343–345)
- Keyword: SHOULD | Actor: Server
- Text: "..."

### REQ-CORE-003 — Third rule
- Section: §10.2 (line 595)
- Keyword: OPTIONAL | Actor: Token
- Text: "..."

### REQ-CORE-IMP-001 — Implicit rule
- Section: §10 (lines 527–528)
- Text: "..."

### REQ-CORE-DEC-001 — A decision
- Some rationale text.
`

const fixtureTestFile = `package fixture

import "testing"

// TestCovered checks the first rule.
//
// Verifies: REQ-CORE-001
func TestCovered(t *testing.T) {}

// TestCoveredSecond checks the second rule.
//
// Verifies: REQ-CORE-002
func TestCoveredSecond(t *testing.T) {}

// TestCoveredThird checks the third rule.
//
// Verifies: REQ-CORE-003
func TestCoveredThird(t *testing.T) {}

// helper has a tag but is not a test function, so it is ignored.
//
// Verifies: REQ-CORE-DEC-001
func helper() {}

// TestUntagged has no tag.
func TestUntagged(t *testing.T) {}
`

const fixtureWaivers = `# Waivers

### REQ-CORE-IMP-001
- Reason: covered-by
- Rationale: restates REQ-CORE-001.
`

func cfgT(t *testing.T) *Config {
	t.Helper()
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Default config invalid: %v", err)
	}
	return &cfg
}

// writeRepo lays out a minimal repository for a core run to scan.
func writeRepo(t *testing.T, catalog, testFile, waivers string) string {
	t.Helper()
	root := t.TempDir()
	mustWriteFile(t, root, "spec/requirements.md", catalog)
	if testFile != "" {
		mustWriteFile(t, root, "pkg_test.go", testFile)
	}
	if waivers != "" {
		mustWriteFile(t, root, "spec/waivers.md", waivers)
	}
	return root
}

func mustWriteFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runCore runs Check over the conventional spec/* layout under the default
// dialect, mirroring the original run() helper.
func runCore(root, out string, strict bool, w io.Writer) error {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		return err
	}
	return Check(&cfg, Scope{
		Root:           root,
		Catalog:        filepath.Join(root, "spec", "requirements.md"),
		Classification: filepath.Join(root, "spec", "classification.md"),
		Waivers:        filepath.Join(root, "spec", "waivers.md"),
		Out:            out,
		Strict:         strict,
	}, w)
}

func TestParseCatalog(t *testing.T) {
	root := writeRepo(t, fixtureCatalog, "", "")
	reqs, problems, err := ParseCatalog(cfgT(t), filepath.Join(root, "spec", "requirements.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Errorf("unexpected problems: %v", problems)
	}
	if len(reqs) != 5 {
		t.Fatalf("got %d requirements, want 5", len(reqs))
	}
	want := []struct{ id, class, section string }{
		{"REQ-CORE-001", "must", "§4"},
		{"REQ-CORE-002", "should", "§7"},
		{"REQ-CORE-003", "may", "§10.2"},
		{"REQ-CORE-IMP-001", "implicit", "§10"},
		{"REQ-CORE-DEC-001", "decision", ""},
	}
	for i, w := range want {
		if reqs[i].ID != w.id || reqs[i].Class != w.class || reqs[i].Section != w.section {
			t.Errorf("reqs[%d] = {%s %s %q}, want {%s %s %q}",
				i, reqs[i].ID, reqs[i].Class, reqs[i].Section, w.id, w.class, w.section)
		}
	}
}

func TestParseCatalogFlagsMalformedHeadings(t *testing.T) {
	bad := fixtureCatalog + "\n### REQ-CORE-1234 — Too many digits\n- Keyword: MUST | Actor: Server\n"
	root := writeRepo(t, bad, "", "")
	reqs, problems, err := ParseCatalog(cfgT(t), filepath.Join(root, "spec", "requirements.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 5 {
		t.Errorf("malformed heading parsed as a requirement: %d reqs", len(reqs))
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "REQ-CORE-1234") {
		t.Errorf("malformed heading not reported: %v", problems)
	}
}

func TestParseCatalogFlagsDuplicateRequirementIDs(t *testing.T) {
	dup := fixtureCatalog + `
### REQ-CORE-001 — Duplicate first rule
- Section: §99
- Keyword: MUST
`
	root := writeRepo(t, dup, "", "")
	reqs, problems, err := ParseCatalog(cfgT(t), filepath.Join(root, "spec", "requirements.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 6 {
		t.Fatalf("got %d parsed requirements, want 6 so the duplicate remains inspectable", len(reqs))
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "duplicate requirement ID REQ-CORE-001") {
		t.Fatalf("duplicate catalog ID not reported: %v", problems)
	}
}

func TestCheckRejectsDuplicateRequirementIDsWithoutWritingMatrix(t *testing.T) {
	dup := fixtureCatalog + `
### REQ-CORE-001 — Duplicate first rule
- Section: §99
- Keyword: MUST
`
	root := writeRepo(t, dup, fixtureTestFile, fixtureWaivers)
	var out strings.Builder
	err := runCore(root, "docs/traceability.md", false, &out)
	if err == nil || !strings.Contains(out.String(), "duplicate requirement ID REQ-CORE-001") {
		t.Fatalf("duplicate catalog passed Check: %v\n%s", err, out.String())
	}
	if _, statErr := os.Stat(filepath.Join(root, "docs", "traceability.md")); !os.IsNotExist(statErr) {
		t.Fatalf("matrix written from duplicate catalog, stat error = %v", statErr)
	}
}

func TestActiveClassificationRejectsUnknownCoverageClass(t *testing.T) {
	cfg := Default()
	cfg.Classification.Values = []ClassValue{{
		Name:                 "not-observable",
		ForbidsCoverageClass: "black-box",
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	root := writeRepo(t, `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
`, "", "")
	mustWriteFile(t, root, "spec/classification.md", `# Classification
### REQ-CORE-001
- Class: not-observable
`)
	var out strings.Builder
	err := Check(&cfg, Scope{
		Root:           root,
		Catalog:        filepath.Join(root, "spec", "requirements.md"),
		Classification: filepath.Join(root, "spec", "classification.md"),
	}, &out)
	if err == nil || !strings.Contains(out.String(), `classification.values[0]: unknown coverage class "black-box"`) {
		t.Fatalf("active classification accepted unknown coverage class: %v\n%s", err, out.String())
	}
}

func TestParseWaiversFlagsMalformedHeadings(t *testing.T) {
	bad := fixtureWaivers + "\n### REQ-CORE-01\n- Reason: covered-by\n- Rationale: x.\n"
	root := writeRepo(t, fixtureCatalog, "", bad)
	waivers, problems, err := ParseWaivers(cfgT(t), filepath.Join(root, "spec", "waivers.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(waivers) != 1 {
		t.Errorf("malformed heading parsed as a waiver: %+v", waivers)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "REQ-CORE-01") {
		t.Errorf("malformed heading not reported: %v", problems)
	}
}

func TestCollectTags(t *testing.T) {
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, "")
	// A tag inside testdata/ must be ignored.
	mustWriteFile(t, root, "testdata/ignored_test.go",
		"package ignored\n\nimport \"testing\"\n\n// Verifies: REQ-CORE-003\nfunc TestIgnored(t *testing.T) {}\n")

	tags, problems, err := CollectTags(cfgT(t), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Errorf("unexpected problems: %v", problems)
	}
	want := map[string]string{
		"REQ-CORE-001": "TestCovered",
		"REQ-CORE-002": "TestCoveredSecond",
		"REQ-CORE-003": "TestCoveredThird",
	}
	for id, fn := range want {
		refs := tags[id]
		if len(refs) != 1 || refs[0].Func != fn {
			t.Errorf("tags[%s] = %v, want one ref from %s", id, refs, fn)
		}
	}
	if len(tags["REQ-CORE-DEC-001"]) != 0 {
		t.Errorf("tag on non-test helper was counted: %v", tags["REQ-CORE-DEC-001"])
	}
}

func TestCollectTagsClassifiesByPath(t *testing.T) {
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, "")
	scenario := "package compliance\n\nimport \"testing\"\n\n// Verifies: REQ-CORE-002\nfunc TestScenario(t *testing.T) {}\n\n// Verifies: REQ-CORE-003\nfunc TestHarnessInternals(t *testing.T) {}\n"
	mustWriteFile(t, root, "compliance/scenario_test.go", scenario)
	tags, problems, err := CollectTags(cfgT(t), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	want := map[string]bool{"unit": false, "blackbox": false}
	for _, ref := range tags["REQ-CORE-002"] {
		want[ref.Class] = true
	}
	if !want["unit"] || !want["blackbox"] {
		t.Errorf("REQ-CORE-002 classes = %v, want both unit and blackbox", tags["REQ-CORE-002"])
	}
	for _, ref := range tags["REQ-CORE-003"] {
		if ref.Func == "TestHarnessInternals" && ref.Class != "unit" {
			t.Errorf("harness self-test classed %q, want unit", ref.Class)
		}
	}
}

func writeClassification(t *testing.T, root, body string) {
	t.Helper()
	mustWriteFile(t, root, "spec/classification.md", body)
}

func addScenario(t *testing.T, root, fn, id string) {
	t.Helper()
	body := "package compliance\n\nimport \"testing\"\n\n// Verifies: " + id + "\nfunc " + fn + "(t *testing.T) {}\n"
	mustWriteFile(t, root, "compliance/"+fn+"_test.go", body)
}

const validClassification = `# Classification

### REQ-CORE-001
- Class: not-observable
- Reason: deployment topology.

### REQ-CORE-002
- Class: wire-observable

### REQ-CORE-003
- Class: not-observable
- Reason: internal decision.

### REQ-CORE-IMP-001
- Class: not-observable
- Reason: implicit.

### REQ-CORE-DEC-001
- Class: not-observable
- Reason: decision.
`

const classTail = "\n### REQ-CORE-IMP-001\n- Class: not-observable\n- Reason: i.\n\n### REQ-CORE-DEC-001\n- Class: not-observable\n- Reason: d.\n"

func TestClassificationAbsentIsDormant(t *testing.T) {
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, "")
	if err := runCore(root, "", false, io.Discard); err != nil {
		t.Errorf("no classification file should be dormant, got: %v", err)
	}
}

func TestClassificationExistingEmptyFileIsActive(t *testing.T) {
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, "")
	writeClassification(t, root, "# Classification\n")
	var out strings.Builder
	err := runCore(root, "", false, &out)
	if err == nil || !strings.Contains(out.String(), "not classified") {
		t.Fatalf("existing empty classification file treated as dormant: %v\n%s", err, out.String())
	}
}

func TestClassificationValidPasses(t *testing.T) {
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, "")
	addScenario(t, root, "TestScenarioTwo", "REQ-CORE-002")
	writeClassification(t, root, validClassification)
	if err := runCore(root, "", false, io.Discard); err != nil {
		t.Errorf("valid classification: run = %v", err)
	}
}

func TestClassificationRequiresEveryRequirement(t *testing.T) {
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, "")
	body := "# C\n\n### REQ-CORE-001\n- Class: not-observable\n- Reason: x.\n\n### REQ-CORE-002\n- Class: not-observable\n- Reason: y.\n" + classTail
	writeClassification(t, root, body)
	err := runCore(root, "", false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "1 problem") {
		t.Errorf("unclassified requirement: run = %v, want a problem", err)
	}
}

func TestClassificationStaleBlackboxTag(t *testing.T) {
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, "")
	addScenario(t, root, "TestScenarioOne", "REQ-CORE-001")
	writeClassification(t, root, validClassification)
	err := runCore(root, "", false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "problem") {
		t.Errorf("stale not-observable classification: run = %v, want a problem", err)
	}
}

func TestClassificationStrictRequiresScenario(t *testing.T) {
	// Cover every requirement so the strict *uncovered* check cannot fire:
	// 001/002/003 are tagged by the fixture; IMP-001/DEC-001 are waived. The
	// only thing left for strict to catch is REQ-CORE-002 — classified
	// wire-observable but carrying just a unit test, no black-box scenario.
	// (The original test left IMP/DEC uncovered, so it passed under
	// strict for the wrong reason and never isolated this behaviour.)
	waivers := `# Waivers

### REQ-CORE-IMP-001
- Reason: covered-by
- Rationale: restates REQ-CORE-001.

### REQ-CORE-DEC-001
- Reason: not-implemented
- Rationale: out of scope.
`
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, waivers)
	writeClassification(t, root, validClassification)
	if err := runCore(root, "", false, io.Discard); err != nil {
		t.Errorf("default mode should not require scenarios: %v", err)
	}
	var out strings.Builder
	if err := runCore(root, "", true, &out); err == nil {
		t.Fatalf("strict: wire-observable without a scenario should fail, got nil\n%s", out.String())
	}
	// The failure must be the missing scenario for the wire-observable
	// requirement, not an unrelated uncovered requirement.
	if !strings.Contains(out.String(), "REQ-CORE-002") || !strings.Contains(out.String(), "wire-observable but has no") {
		t.Errorf("strict failure is not the missing-scenario problem for REQ-CORE-002:\n%s", out.String())
	}
	if strings.Contains(out.String(), "no tagged test or waiver") {
		t.Errorf("strict failed on an uncovered requirement, not the scenario rule:\n%s", out.String())
	}
}

func TestMatrixHasGapLists(t *testing.T) {
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, "")
	mustWriteFile(t, root, "compliance/scenario_test.go",
		"package compliance\n\nimport \"testing\"\n\n// Verifies: REQ-CORE-002\nfunc TestScenario(t *testing.T) {}\n")
	if err := runCore(root, filepath.Join("docs", "traceability.md"), false, io.Discard); err != nil {
		t.Fatalf("run = %v", err)
	}
	matrix, err := os.ReadFile(filepath.Join(root, "docs", "traceability.md")) // #nosec G304 -- reads from t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	m := string(matrix)
	if !strings.Contains(m, "Coverage by test class:") {
		t.Errorf("matrix lacks the gap-list section:\n%s", m)
	}
	for _, want := range []string{
		"- both (unit + black-box): 1 — REQ-CORE-002",
		"- unit only: ",
		"REQ-CORE-003",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("gap lists missing %q:\n%s", want, m)
		}
	}
}

func TestMatrixHasPerClassColumns(t *testing.T) {
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, "")
	mustWriteFile(t, root, "compliance/scenario_test.go",
		"package compliance\n\nimport \"testing\"\n\n// Verifies: REQ-CORE-002\nfunc TestScenario(t *testing.T) {}\n")
	if err := runCore(root, filepath.Join("docs", "traceability.md"), false, io.Discard); err != nil {
		t.Fatalf("run = %v", err)
	}
	matrix, err := os.ReadFile(filepath.Join(root, "docs", "traceability.md")) // #nosec G304 -- reads from t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	m := string(matrix)
	if !strings.Contains(m, "| Unit coverage | Black-box coverage |") {
		t.Errorf("matrix lacks per-class columns:\n%s", m)
	}
	for _, want := range []string{"`TestCoveredSecond`", "`TestScenario` (compliance/scenario_test.go)"} {
		if !strings.Contains(m, want) {
			t.Errorf("matrix missing %s:\n%s", want, m)
		}
	}
}

func TestCollectTagsRejectsMultipleRequirements(t *testing.T) {
	file := `package fixture

import "testing"

// Verifies: REQ-CORE-001, REQ-CORE-002
func TestTwoOnOneLine(t *testing.T) {}

// Verifies: REQ-CORE-001
// Verifies: REQ-CORE-003
func TestTwoAcrossLines(t *testing.T) {}

// Verifies: REQ-CORE-002
func TestSingle(t *testing.T) {}
`
	root := writeRepo(t, fixtureCatalog, file, "")
	tags, problems, err := CollectTags(cfgT(t), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want exactly 2 (one per multi-tag test)", problems)
	}
	for i, fn := range []string{"TestTwoOnOneLine", "TestTwoAcrossLines"} {
		if !strings.Contains(problems[i], fn) {
			t.Errorf("problems[%d] = %q, want it to name %s", i, problems[i], fn)
		}
	}
	if len(tags["REQ-CORE-002"]) != 2 {
		t.Errorf("tags[REQ-CORE-002] = %v, want refs from both tagging tests", tags["REQ-CORE-002"])
	}
}

func TestCollectTagsIgnoresMethods(t *testing.T) {
	file := `package fixture

import "testing"

type suite struct{}

// TestShaped is a method, not a test: the runner never executes it, so its
// tag must not count as coverage.
//
// Verifies: REQ-CORE-001
func (s suite) TestShaped(t *testing.T) {}
`
	root := writeRepo(t, fixtureCatalog, file, "")
	tags, problems, err := CollectTags(cfgT(t), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Errorf("unexpected problems: %v", problems)
	}
	if len(tags) != 0 {
		t.Errorf("method tag counted as coverage: %v", tags)
	}
}

func TestParseTagLine(t *testing.T) {
	cfg := cfgT(t)
	cases := []struct {
		line    string
		wantIDs int
		wantErr bool
	}{
		{"// Verifies: REQ-CORE-001", 1, false},
		{"// Verifies: REQ-CORE-001, REQ-CORE-DEC-002, REQ-CORE-IMP-003", 3, false},
		{"// no tag here", 0, false},
		{"// Verifies: REQ-CORE-01", 0, true},
		{"// Verifies: REQ-CORE-1234", 0, true},
		{"// Verifies: REQ-CORE-001x", 0, true},
		{"// Verifies: REQ-CORE-001; REQ-CORE-002", 0, true},
		{"// Verifies:", 0, true},
		{"// see also Verifies: REQ-CORE-001", 0, true},
	}
	for _, c := range cases {
		ids, problem := cfg.parseTagLine(c.line)
		if len(ids) != c.wantIDs {
			t.Errorf("parseTagLine(%q) ids = %v, want %d", c.line, ids, c.wantIDs)
		}
		if (problem != "") != c.wantErr {
			t.Errorf("parseTagLine(%q) problem = %q, wantErr %v", c.line, problem, c.wantErr)
		}
	}
}

func TestParseWaivers(t *testing.T) {
	root := writeRepo(t, fixtureCatalog, "", fixtureWaivers)
	waivers, _, err := ParseWaivers(cfgT(t), filepath.Join(root, "spec", "waivers.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(waivers) != 1 || waivers[0].ID != "REQ-CORE-IMP-001" || waivers[0].Reason != "covered-by" {
		t.Fatalf("waivers = %+v, want one covered-by entry for REQ-CORE-IMP-001", waivers)
	}
	if !waivers[0].HasRationale {
		t.Error("rationale line not detected")
	}
}

func TestParseWaiversRejectsTrailingJunkInReason(t *testing.T) {
	root := writeRepo(t, fixtureCatalog, "", "### REQ-CORE-001\n- Reason: covered-by extra-text\n- Rationale: x.\n")
	cfg := cfgT(t)
	waivers, _, err := ParseWaivers(cfg, filepath.Join(root, "spec", "waivers.md"))
	if err != nil {
		t.Fatal(err)
	}
	valid := map[string]bool{}
	for _, r := range cfg.Waivers.Reasons {
		valid[r] = true
	}
	if len(waivers) != 1 || valid[waivers[0].Reason] {
		t.Fatalf("reason with trailing junk should be invalid, got %+v", waivers)
	}
}

func TestRunIntegrityPassesWithUncovered(t *testing.T) {
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, fixtureWaivers)
	if err := runCore(root, "docs/traceability.md", false, &strings.Builder{}); err != nil {
		t.Fatalf("default mode failed: %v", err)
	}
	matrix, err := os.ReadFile(filepath.Join(root, "docs", "traceability.md")) // #nosec G304 -- reads from t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"REQ-CORE-001", "TestCovered", "waiver: covered-by", "must: 1/1"} {
		if !strings.Contains(string(matrix), want) {
			t.Errorf("matrix missing %q", want)
		}
	}
}

func TestRunStrictFailsOnUncovered(t *testing.T) {
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, fixtureWaivers)
	var out strings.Builder
	err := runCore(root, "", true, &out)
	if err == nil {
		t.Fatal("strict mode passed despite uncovered REQ-CORE-DEC-001")
	}
	if !strings.Contains(out.String(), "REQ-CORE-DEC-001") {
		t.Errorf("strict failure does not name the uncovered requirement: %s", out.String())
	}
}

func TestRunFlagsIntegrityProblems(t *testing.T) {
	badTest := strings.Replace(fixtureTestFile, "REQ-CORE-001", "REQ-CORE-999", 1)
	badWaivers := fixtureWaivers + `
### REQ-CORE-002
- Reason: because
- Rationale: tagged and waived, with an invalid reason.

### REQ-CORE-003
- Reason: not-implemented

### REQ-CORE-IMP-001
- Reason: covered-by
- Rationale: duplicate of the entry above.
`
	root := writeRepo(t, fixtureCatalog, badTest, badWaivers)
	var out strings.Builder
	err := runCore(root, "docs/traceability.md", false, &out)
	if err == nil {
		t.Fatal("integrity problems not reported")
	}
	got := out.String()
	for _, want := range []string{
		"REQ-CORE-999",
		"invalid waiver reason",
		"both a waiver and tagged",
		"no Rationale line",
		"duplicate waiver",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if _, statErr := os.Stat(filepath.Join(root, "docs", "traceability.md")); !os.IsNotExist(statErr) {
		t.Error("matrix was written despite integrity problems")
	}
}

func TestRunFlagsMalformedTags(t *testing.T) {
	badTest := strings.Replace(fixtureTestFile, "REQ-CORE-001\n", "REQ-CORE-001x\n", 1)
	root := writeRepo(t, fixtureCatalog, badTest, fixtureWaivers)
	var out strings.Builder
	err := runCore(root, "", false, &out)
	if err == nil {
		t.Fatal("malformed tag not reported")
	}
	if !strings.Contains(out.String(), "malformed tag line") {
		t.Errorf("output missing malformed-tag problem:\n%s", out.String())
	}
}

// --- REQ-API series / multi-scope generalization (default dialect) ---

const fixtureAPICatalog = `# Server catalog

### REQ-API-001 — Publish resource list
- Section: spec §4.1.1
- Keyword: MUST | Actor: Server
- Text: "..."

### REQ-API-002 — Bounded cache
- Section: spec §5.4.2
- Keyword: MUST | Actor: Validator
- Text: "..."
`

const fixtureAPIClass = `# Server classification

### REQ-API-001
- Class: wire-observable

### REQ-API-002
- Class: not-observable
- Reason: cache timing.
`

const fixtureAPITest = `package srv

import "testing"

// Verifies: REQ-API-001
func TestAPIPublish(t *testing.T) {}

// Verifies: REQ-API-002
func TestAPICache(t *testing.T) {}
`

func TestParseCatalogServerSeries(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, root, "server/spec/requirements.md", fixtureAPICatalog)
	reqs, problems, err := ParseCatalog(cfgT(t), filepath.Join(root, "server", "spec", "requirements.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Errorf("unexpected problems: %v", problems)
	}
	if len(reqs) != 2 {
		t.Fatalf("got %d requirements, want 2", len(reqs))
	}
	if reqs[0].ID != "REQ-API-001" || reqs[0].Class != "must" || reqs[0].Section != "§4.1.1" {
		t.Errorf("reqs[0] = {%s %s %q}, want {REQ-API-001 must §4.1.1}", reqs[0].ID, reqs[0].Class, reqs[0].Section)
	}
}

func TestParseTagLineServerSeries(t *testing.T) {
	ids, problem := cfgT(t).parseTagLine("// Verifies: REQ-API-001")
	if problem != "" {
		t.Fatalf("unexpected problem: %s", problem)
	}
	if len(ids) != 1 || ids[0] != "REQ-API-001" {
		t.Errorf("ids = %v, want [REQ-API-001]", ids)
	}
}

func TestRunScopeServer(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, root, "server/spec/requirements.md", fixtureAPICatalog)
	mustWriteFile(t, root, "server/spec/classification.md", fixtureAPIClass)
	mustWriteFile(t, root, "server/keys_test.go", fixtureAPITest)
	cfg := cfgT(t)
	var out strings.Builder
	err := Check(cfg, Scope{
		Root:           root,
		Catalog:        filepath.Join(root, "server", "spec", "requirements.md"),
		Classification: filepath.Join(root, "server", "spec", "classification.md"),
		Waivers:        filepath.Join(root, "server", "spec", "waivers.md"),
	}, &out)
	if err != nil {
		t.Fatalf("Check: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "2 requirements, 2 covered") {
		t.Errorf("summary = %q, want 2 requirements, 2 covered", out.String())
	}
}

func TestRunCoreIgnoresServerTags(t *testing.T) {
	extra := fixtureTestFile + "\n// Verifies: REQ-API-001\nfunc TestServerThing(t *testing.T) {}\n"
	root := writeRepo(t, fixtureCatalog, extra, fixtureWaivers)
	var out strings.Builder
	if err := runCore(root, "", false, &out); err != nil {
		t.Fatalf("core run errored on a server-series tag: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "REQ-API-001") {
		t.Errorf("core run flagged a server-series tag:\n%s", out.String())
	}
}

func TestParseTagLineRejectsBadSeriesAndDigits(t *testing.T) {
	cfg := cfgT(t)
	for _, bad := range []string{
		"// Verifies: REQ-foo-001", // lowercase series is not a valid shape
		"// Verifies: REQ--001",    // empty series
		"// Verifies: REQ-API-9999",
		"// Verifies: REQ-API-01",
	} {
		ids, problem := cfg.parseTagLine(bad)
		if problem == "" || len(ids) != 0 {
			t.Errorf("%q: ids=%v problem=%q, want a malformed-tag problem and no ids", bad, ids, problem)
		}
	}
	// The default grammar no longer whitelists specific series: any uppercase
	// series of the right shape is accepted (scope filtering, not the grammar,
	// decides which series a run cares about).
	ids, problem := cfg.parseTagLine("// Verifies: REQ-FOO-001")
	if problem != "" || len(ids) != 1 || ids[0] != "REQ-FOO-001" {
		t.Errorf("arbitrary uppercase series: ids=%v problem=%q, want [REQ-FOO-001] and no problem", ids, problem)
	}
}

func TestRunScopeServerFlagsUnknownAPI(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, root, "server/spec/requirements.md", fixtureAPICatalog)
	mustWriteFile(t, root, "server/spec/classification.md", fixtureAPIClass)
	mustWriteFile(t, root, "server/x_test.go",
		"package srv\n\nimport \"testing\"\n\n// Verifies: REQ-API-999\nfunc TestUnknown(t *testing.T) {}\n")
	cfg := cfgT(t)
	var out strings.Builder
	err := Check(cfg, Scope{
		Root:           root,
		Catalog:        filepath.Join(root, "server", "spec", "requirements.md"),
		Classification: filepath.Join(root, "server", "spec", "classification.md"),
		Waivers:        filepath.Join(root, "server", "spec", "waivers.md"),
	}, &out)
	if err == nil {
		t.Fatalf("expected a problem for the unknown API tag:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "REQ-API-999") || !strings.Contains(out.String(), "not in the catalog") {
		t.Errorf("output missing unknown-API problem:\n%s", out.String())
	}
}
