package tracecheck

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const archFixture = `# Architecture

### Components

- fault-in
- cas

### Invariants

- I-VERIFY — no exposure before verify
- I-BASE-UNIT
`

func TestLoadArchitecture(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "architecture.md")
	if err := os.WriteFile(path, []byte(archFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := cfgT(t)
	arch, problems, err := LoadArchitecture(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if !arch.HasComponent("fault-in") || !arch.HasInvariant("I-VERIFY") {
		t.Fatalf("arch = %+v", arch)
	}
	if arch.HasComponent("missing") {
		t.Fatal("missing component reported present")
	}
}

func TestCatalogMetaRequiredAndEnum(t *testing.T) {
	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{
		{Name: "Phase", Required: true, Enum: []string{"1", "2"}},
		{Name: "Kind", Required: true, Enum: []string{"encoding", "ops"}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	catalog := `# Catalog
### REQ-CORE-001 — ok
- Section: §1
- Keyword: MUST
- Phase: 1
- Kind: encoding

### REQ-CORE-002 — bad phase
- Section: §1
- Keyword: MUST
- Phase: 9
- Kind: encoding

### REQ-CORE-003 — missing kind
- Section: §1
- Keyword: MUST
- Phase: 1
`
	root := writeRepo(t, catalog, fixtureTestFile, fixtureWaivers)
	// Tag only 001 so we don't care about coverage here.
	var out strings.Builder
	err := Check(&cfg, Scope{
		Root:    root,
		Catalog: filepath.Join(root, "spec", "requirements.md"),
		Waivers: filepath.Join(root, "spec", "waivers.md"),
		Out:     "",
		Strict:  false,
	}, &out)
	if err == nil {
		t.Fatalf("expected problems, got clean: %s", out.String())
	}
	s := out.String()
	if !strings.Contains(s, `REQ-CORE-002: invalid Phase "9"`) {
		t.Errorf("missing phase enum error: %s", s)
	}
	if !strings.Contains(s, "REQ-CORE-003: missing required catalog field Kind") {
		t.Errorf("missing required field error: %s", s)
	}
}

func TestCatalogMetaEnumFromArchitecture(t *testing.T) {
	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{
		{Name: "Component", Required: true, EnumFrom: "architecture.components"},
		{Name: "Invariant", Required: false, EnumFrom: "architecture.invariants"},
	}
	cfg.Architecture.Path = "docs/architecture.md"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	catalog := `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
- Component: fault-in
- Invariant: I-VERIFY

### REQ-CORE-002
- Section: §1
- Keyword: MUST
- Component: not-a-component
`
	root := writeRepo(t, catalog,
		"package p\nimport \"testing\"\n// Verifies: REQ-CORE-001\nfunc TestA(t *testing.T) {}\n// Verifies: REQ-CORE-002\nfunc TestB(t *testing.T) {}\n",
		"")
	mustWriteFile(t, root, "docs/architecture.md", archFixture)

	var out strings.Builder
	err := Check(&cfg, Scope{
		Root:    root,
		Catalog: filepath.Join(root, "spec", "requirements.md"),
		Out:     "",
	}, &out)
	if err == nil {
		t.Fatalf("expected problems: %s", out.String())
	}
	if !strings.Contains(out.String(), `REQ-CORE-002: invalid Component "not-a-component"`) {
		t.Errorf("got: %s", out.String())
	}
}

func TestPolicyRejectsUnknownArchitectureMetadataValue(t *testing.T) {
	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{
		{Name: "Component", Required: true, EnumFrom: "architecture.components"},
	}
	cfg.Architecture.Path = "docs/architecture.md"
	cfg.Policy.Rules = []PolicyRule{{
		When:                        map[string][]string{"Component": {"typo-component"}},
		StrictRequiresCoverageClass: "unit",
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	catalog := `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
- Component: fault-in
`
	root := writeRepo(t, catalog, "", "")
	mustWriteFile(t, root, "docs/architecture.md", archFixture)

	var out strings.Builder
	err := Check(&cfg, Scope{
		Root:    root,
		Catalog: filepath.Join(root, "spec", "requirements.md"),
	}, &out)
	if err == nil || !strings.Contains(out.String(), `policy.rules[0].when: unknown Component value "typo-component"`) {
		t.Fatalf("unknown architecture policy value accepted: %v\n%s", err, out.String())
	}
}

func TestPhaseAwareStrict(t *testing.T) {
	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{
		{Name: "Phase", Required: true, Enum: []string{"1", "2"}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	catalog := `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
- Phase: 1

### REQ-CORE-002
- Section: §1
- Keyword: MUST
- Phase: 2

### REQ-CORE-003
- Section: §1
- Keyword: SHOULD
- Phase: 1
`
	// Only tag 001; under phase-1 must-only strict, 002 and 003 are out of scope.
	tests := `package p
import "testing"
// Verifies: REQ-CORE-001
func TestOne(t *testing.T) {}
`
	root := writeRepo(t, catalog, tests, "")
	var out strings.Builder
	err := Check(&cfg, Scope{
		Root:                 root,
		Catalog:              filepath.Join(root, "spec", "requirements.md"),
		Strict:               true,
		StrictPhases:         []string{"1"},
		StrictKeywordClasses: []string{"must"},
	}, &out)
	if err != nil {
		t.Fatalf("expected clean phase1-must: %v\n%s", err, out.String())
	}

	// Without filters, 002 and 003 are uncovered under full strict.
	out.Reset()
	err = Check(&cfg, Scope{
		Root:    root,
		Catalog: filepath.Join(root, "spec", "requirements.md"),
		Strict:  true,
	}, &out)
	if err == nil {
		t.Fatal("expected full-strict failures")
	}
	if !strings.Contains(out.String(), "REQ-CORE-002") || !strings.Contains(out.String(), "REQ-CORE-003") {
		t.Errorf("expected 002 and 003 uncovered: %s", out.String())
	}
}

func TestStructuredCoveredBy(t *testing.T) {
	cfg := Default()
	cfg.Waivers.RequireCoversForCoveredBy = true
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	catalog := `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
### REQ-CORE-002
- Section: §1
- Keyword: MUST
`
	// Missing Covers when required.
	waivers := `### REQ-CORE-002
- Reason: covered-by
- Rationale: should name covers.
`
	tests := `package p
import "testing"
// Verifies: REQ-CORE-001
func TestOne(t *testing.T) {}
`
	root := writeRepo(t, catalog, tests, waivers)
	var out strings.Builder
	err := Check(&cfg, Scope{
		Root:    root,
		Catalog: filepath.Join(root, "spec", "requirements.md"),
		Waivers: filepath.Join(root, "spec", "waivers.md"),
		Strict:  true,
	}, &out)
	if err == nil || !strings.Contains(out.String(), "covered-by waiver has no Covers line") {
		t.Fatalf("want missing Covers error, got: %v\n%s", err, out.String())
	}

	// Valid Covers target.
	waivers = `### REQ-CORE-002
- Reason: covered-by
- Covers: REQ-CORE-001
- Rationale: special case of 001.
`
	root = writeRepo(t, catalog, tests, waivers)
	out.Reset()
	err = Check(&cfg, Scope{
		Root:    root,
		Catalog: filepath.Join(root, "spec", "requirements.md"),
		Waivers: filepath.Join(root, "spec", "waivers.md"),
		Out:     "docs/traceability.md",
		Strict:  true,
	}, &out)
	if err != nil {
		t.Fatalf("expected clean: %v\n%s", err, out.String())
	}
	matrix, err := os.ReadFile(filepath.Join(root, "docs", "traceability.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(matrix), "waiver: covered-by → REQ-CORE-001") {
		t.Errorf("matrix missing structured waiver cell: %s", matrix)
	}
}

func TestCoveredByCycle(t *testing.T) {
	cfg := cfgT(t)
	catalog := `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
### REQ-CORE-002
- Section: §1
- Keyword: MUST
`
	waivers := `### REQ-CORE-001
- Reason: covered-by
- Covers: REQ-CORE-002
- Rationale: a.
### REQ-CORE-002
- Reason: covered-by
- Covers: REQ-CORE-001
- Rationale: b.
`
	root := writeRepo(t, catalog, "", waivers)
	var out strings.Builder
	err := Check(cfg, Scope{
		Root:    root,
		Catalog: filepath.Join(root, "spec", "requirements.md"),
		Waivers: filepath.Join(root, "spec", "waivers.md"),
	}, &out)
	if err == nil || !strings.Contains(out.String(), "covered-by cycle") {
		t.Fatalf("want cycle error: %v\n%s", err, out.String())
	}
}

func TestPolicyRules(t *testing.T) {
	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{
		{Name: "Kind", Required: true, Enum: []string{"encoding", "ops"}},
	}
	cfg.Coverage.Rules = []CoverageRule{
		{Class: "conformance", PathPrefixes: []string{"conformance/"}},
	}
	cfg.Policy.Rules = []PolicyRule{
		{When: map[string][]string{"Kind": {"encoding"}}, StrictRequiresCoverageClass: "conformance"},
		{When: map[string][]string{"Kind": {"ops"}}, AllowUncovered: true},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	catalog := `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
- Kind: encoding
### REQ-CORE-002
- Section: §1
- Keyword: MUST
- Kind: ops
`
	// Unit tag only for encoding req — policy requires conformance under strict.
	tests := `package p
import "testing"
// Verifies: REQ-CORE-001
func TestUnit(t *testing.T) {}
`
	root := writeRepo(t, catalog, tests, "")
	var out strings.Builder
	err := Check(&cfg, Scope{
		Root:    root,
		Catalog: filepath.Join(root, "spec", "requirements.md"),
		Strict:  true,
	}, &out)
	if err == nil || !strings.Contains(out.String(), "policy requires conformance coverage") {
		t.Fatalf("want policy error: %v\n%s", err, out.String())
	}
	// ops may be uncovered.
	if strings.Contains(out.String(), "REQ-CORE-002") && strings.Contains(out.String(), "no tagged test") {
		t.Errorf("ops should allow uncovered: %s", out.String())
	}

	// Conformance tag satisfies policy; ops still uncovered OK.
	mustWriteFile(t, root, "conformance/enc_test.go",
		"package conformance\nimport \"testing\"\n// Verifies: REQ-CORE-001\nfunc TestEnc(t *testing.T) {}\n")
	// Remove unit-only file conflict: REQ-CORE-001 cannot have waiver and tags;
	// two tags for same ID is OK. But we still have TestUnit - fine, multiple tags ok.
	// Wait - one test can only tag one req, but one req can have multiple tests. Good.
	// However TestUnit still tags 001 with unit class, and TestEnc with conformance - good.
	out.Reset()
	err = Check(&cfg, Scope{
		Root:    root,
		Catalog: filepath.Join(root, "spec", "requirements.md"),
		Strict:  true,
	}, &out)
	if err != nil {
		t.Fatalf("expected clean after conformance tag: %v\n%s", err, out.String())
	}
}

func TestProfileAndOutJSON(t *testing.T) {
	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{
		{Name: "Phase", Required: true, Enum: []string{"1", "2"}},
	}
	cfg.Profiles = map[string]Profile{
		"phase1-freeze": {
			Strict:               true,
			StrictPhases:         []string{"1"},
			StrictKeywordClasses: []string{"must"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	catalog := `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
- Phase: 1
### REQ-CORE-002
- Section: §1
- Keyword: MUST
- Phase: 2
`
	tests := `package p
import "testing"
// Verifies: REQ-CORE-001
func TestOne(t *testing.T) {}
`
	root := writeRepo(t, catalog, tests, "")
	scope := Scope{
		Root:    root,
		Catalog: filepath.Join(root, "spec", "requirements.md"),
		Out:     "docs/traceability.md",
		OutJSON: "docs/traceability.json",
	}
	if err := cfg.ApplyProfile("phase1-freeze", &scope); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := Check(&cfg, scope, &out); err != nil {
		t.Fatalf("profile run: %v\n%s", err, out.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "traceability.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mj MatrixJSON
	if err := json.Unmarshal(data, &mj); err != nil {
		t.Fatal(err)
	}
	if mj.Summary.Total != 2 || mj.Summary.Tagged != 1 {
		t.Fatalf("json summary = %+v", mj.Summary)
	}
	if strings.Contains(mj.GeneratedBy, "`") {
		t.Fatalf("JSON generatedBy contains Markdown quoting: %q", mj.GeneratedBy)
	}
	if mj.Requirements[0].Meta["Phase"] != "1" && mj.Requirements[1].Meta["Phase"] != "1" {
		t.Fatalf("meta not in json: %+v", mj.Requirements)
	}
}

func TestProblemsJSONWrittenOnStrictFailure(t *testing.T) {
	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{{Name: "Phase", Required: true, Enum: []string{"1"}}}
	cfg.Profiles = map[string]Profile{
		"phase1-freeze": {Strict: true, StrictPhases: []string{"1"}, StrictKeywordClasses: []string{"must"}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	root := writeRepo(t, `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
- Phase: 1
`, "", "")
	scope := Scope{
		Root:         root,
		Catalog:      filepath.Join(root, "spec", "requirements.md"),
		ProblemsJSON: "docs/problems.json",
		ToolVersion:  "trace-check version=test revision=0123456789abcdef0123456789abcdef01234567 modified=false",
	}
	if err := cfg.ApplyProfile("phase1-freeze", &scope); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := Check(&cfg, scope, &out); err == nil {
		t.Fatalf("strict check unexpectedly passed: %s", out.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report ProblemReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 1 || report.Profile != "phase1-freeze" {
		t.Fatalf("problem report metadata = %+v", report)
	}
	if !report.Complete || report.Artifacts.Catalog == nil || !report.Artifacts.Catalog.Present || report.Artifacts.Catalog.Path != "spec/requirements.md" || !strings.HasPrefix(report.Artifacts.Catalog.SHA256, "sha256:") {
		t.Fatalf("problem report artifact provenance = %+v", report.Artifacts)
	}
	if !report.Scope.Strict || len(report.Scope.Phases) != 1 || report.Scope.Phases[0] != "1" || len(report.Scope.KeywordClasses) != 1 || report.Scope.KeywordClasses[0] != "must" {
		t.Fatalf("problem report scope = %+v", report.Scope)
	}
	if report.ToolVersion != scope.ToolVersion || !strings.HasPrefix(report.ConfigDigest, "sha256:") {
		t.Fatalf("problem report provenance = %+v", report)
	}
	if len(report.Problems) != 1 {
		t.Fatalf("problems = %+v, want one", report.Problems)
	}
	problem := report.Problems[0]
	if problem.Key != "coverage-required:REQ-CORE-001" || problem.Code != "coverage-required" || problem.Requirement != "REQ-CORE-001" || !problem.Baselinable {
		t.Fatalf("problem = %+v", problem)
	}
}

func TestProblemsJSONReplacesStaleReportBeforeInputFailure(t *testing.T) {
	cfg := cfgT(t)
	root := t.TempDir()
	problemPath := filepath.Join(root, "problems.json")
	if err := os.WriteFile(problemPath, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Check(cfg, Scope{
		Root:         root,
		Catalog:      filepath.Join(root, "missing-catalog.md"),
		ProblemsJSON: "problems.json",
	}, io.Discard)
	if err == nil {
		t.Fatal("missing catalog unexpectedly passed")
	}
	data, readErr := os.ReadFile(problemPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var report ProblemReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("stale report was not replaced with valid JSON: %v: %q", err, data)
	}
	if report.Complete || report.Artifacts.Catalog == nil || report.Artifacts.Catalog.Present {
		t.Fatalf("early-failure report = %+v", report)
	}
}

func TestProblemsJSONHashesTheReconciledInputSnapshot(t *testing.T) {
	cfg := cfgT(t)
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, fixtureWaivers)
	catalogPath := filepath.Join(root, "spec", "requirements.md")
	original, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	replacement := append(append([]byte(nil), original...), []byte("\nchanged after reconciliation\n")...)
	out := &mutatingWriter{mutate: func() {
		if err := os.WriteFile(catalogPath, replacement, 0o600); err != nil {
			t.Fatal(err)
		}
	}}
	err = Check(cfg, Scope{
		Root:         root,
		Catalog:      catalogPath,
		Waivers:      filepath.Join(root, "spec", "waivers.md"),
		ProblemsJSON: "docs/problems.json",
	}, out)
	if err != nil {
		t.Fatalf("snapshot-backed check failed after a later edit: %v\n%s", err, out.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report ProblemReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Complete || report.Summary.Total != 0 {
		t.Fatalf("snapshot-backed report = %+v", report)
	}
	originalSum := sha256.Sum256(original)
	wantDigest := "sha256:" + hex.EncodeToString(originalSum[:])
	if report.Artifacts.Catalog == nil || report.Artifacts.Catalog.SHA256 != wantDigest {
		t.Fatalf("catalog provenance = %+v, want pre-check digest %s", report.Artifacts.Catalog, wantDigest)
	}
	replacementSum := sha256.Sum256(replacement)
	if report.Artifacts.Catalog.SHA256 == "sha256:"+hex.EncodeToString(replacementSum[:]) {
		t.Fatal("report hashed the post-reconciliation replacement")
	}
}

func TestProblemsJSONHashesTheConfigBytesLoadConfigDecoded(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "tracecheck.json")
	original := []byte(`{"matrix":{"generatedBy":"loaded source"}}`)
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"matrix":{"generatedBy":"replacement"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, fixtureWaivers)
	if err := Check(&cfg, Scope{
		Root:         root,
		Catalog:      filepath.Join(root, "spec", "requirements.md"),
		Waivers:      filepath.Join(root, "spec", "waivers.md"),
		ConfigPath:   configPath,
		ProblemsJSON: "docs/problems.json",
	}, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report ProblemReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	wantSum := sha256.Sum256(original)
	wantDigest := "sha256:" + hex.EncodeToString(wantSum[:])
	if report.Artifacts.Config == nil || report.Artifacts.Config.SHA256 != wantDigest {
		t.Fatalf("config provenance = %+v, want LoadConfig digest %s", report.Artifacts.Config, wantDigest)
	}
	if report.GeneratedBy != "loaded source" {
		t.Fatalf("effective config came from replacement: generatedBy = %q", report.GeneratedBy)
	}
}

func TestProblemsJSONSupportsCLIShapedRelativeRootInputs(t *testing.T) {
	cfg := cfgT(t)
	absRoot := writeRepo(t, fixtureCatalog, fixtureTestFile, fixtureWaivers)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relRoot, err := filepath.Rel(cwd, absRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(cfg, Scope{
		Root:         relRoot,
		Catalog:      filepath.Join(relRoot, "spec", "requirements.md"),
		Waivers:      filepath.Join(relRoot, "spec", "waivers.md"),
		ProblemsJSON: "docs/problems.json",
	}, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(relRoot, "docs", "problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report ProblemReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Artifacts.Catalog == nil || !report.Artifacts.Catalog.Present || report.Artifacts.Catalog.Path != "spec/requirements.md" {
		t.Fatalf("relative-root catalog provenance = %+v", report.Artifacts.Catalog)
	}
}

func TestProblemsJSONNormalizesSymlinkDotDotBeforeValidationAndIO(t *testing.T) {
	target := t.TempDir()
	configPath := filepath.Join(target, "tracecheck.json")
	configBytes := []byte("{}\n")
	if err := os.WriteFile(configPath, configBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(target, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, fixtureWaivers)
	if err := os.Symlink(filepath.Join(target, "sub"), filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	rawProblemsPath := filepath.Join(root, "escape") + string(filepath.Separator) + ".." + string(filepath.Separator) + "tracecheck.json"
	if err := Check(&cfg, Scope{
		Root:         root,
		Catalog:      filepath.Join(root, "spec", "requirements.md"),
		Waivers:      filepath.Join(root, "spec", "waivers.md"),
		ConfigPath:   configPath,
		ProblemsJSON: rawProblemsPath,
	}, io.Discard); err != nil {
		t.Fatal(err)
	}
	gotConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotConfig) != string(configBytes) {
		t.Fatalf("symlink/.. output overwrote config source: %q", gotConfig)
	}
	normalizedReport := filepath.Join(root, "tracecheck.json")
	data, err := os.ReadFile(normalizedReport)
	if err != nil {
		t.Fatalf("normalized report was not written: %v", err)
	}
	var report ProblemReport
	if err := json.Unmarshal(data, &report); err != nil || !report.Complete {
		t.Fatalf("normalized report = %+v, %v", report, err)
	}
}

func TestProblemsJSONRecordsDeterministicTagEvidence(t *testing.T) {
	cfg := cfgT(t)
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, fixtureWaivers)
	if err := Check(cfg, Scope{
		Root:         root,
		Catalog:      filepath.Join(root, "spec", "requirements.md"),
		Waivers:      filepath.Join(root, "spec", "waivers.md"),
		ProblemsJSON: "docs/problems.json",
	}, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report ProblemReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Evidence.Tags) != 3 {
		t.Fatalf("tag evidence = %+v, want three records", report.Evidence)
	}
	for i, want := range []string{"REQ-CORE-001", "REQ-CORE-002", "REQ-CORE-003"} {
		if report.Evidence.Tags[i].Requirement != want || report.Evidence.Tags[i].File != "pkg_test.go" || report.Evidence.Tags[i].Class != "unit" {
			t.Fatalf("tag evidence[%d] = %+v", i, report.Evidence.Tags[i])
		}
	}
	encoded, err := json.Marshal(report.Evidence.Tags)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	if report.Evidence.TagsSHA256 != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("tag evidence digest = %q", report.Evidence.TagsSHA256)
	}
}

func TestProblemsJSONMarksIntegrityProblemsUnbaselinable(t *testing.T) {
	cfg := cfgT(t)
	root := writeRepo(t, fixtureCatalog+`
### REQ-CORE-001 — duplicate
- Section: §9
- Keyword: MUST
`, fixtureTestFile, fixtureWaivers)
	scope := Scope{
		Root:         root,
		Catalog:      filepath.Join(root, "spec", "requirements.md"),
		Waivers:      filepath.Join(root, "spec", "waivers.md"),
		ProblemsJSON: "docs/problems.json",
	}
	if err := Check(cfg, scope, io.Discard); err == nil {
		t.Fatal("duplicate catalog unexpectedly passed")
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report ProblemReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Problems) != 1 || report.Problems[0].Code != "integrity" || report.Problems[0].Baselinable {
		t.Fatalf("integrity report = %+v", report)
	}
}

func TestProblemsJSONSupportsAbsoluteOutputPath(t *testing.T) {
	cfg := cfgT(t)
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, fixtureWaivers)
	path := filepath.Join(t.TempDir(), "problems.json")
	if err := Check(cfg, Scope{
		Root:         root,
		Catalog:      filepath.Join(root, "spec", "requirements.md"),
		Waivers:      filepath.Join(root, "spec", "waivers.md"),
		ProblemsJSON: path,
	}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("absolute problems path not written: %v", err)
	}
}

func TestCheckOutputDetectsDriftWithoutWriting(t *testing.T) {
	cfg := cfgT(t)
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, fixtureWaivers)
	scope := Scope{
		Root:         root,
		Catalog:      filepath.Join(root, "spec", "requirements.md"),
		Waivers:      filepath.Join(root, "spec", "waivers.md"),
		Out:          "docs/traceability.md",
		OutJSON:      "docs/traceability.json",
		ProblemsJSON: "docs/problems.json",
	}
	if err := Check(cfg, scope, io.Discard); err != nil {
		t.Fatal(err)
	}
	scope.CheckOutput = true
	if err := Check(cfg, scope, io.Discard); err != nil {
		t.Fatalf("fresh generated outputs rejected: %v", err)
	}
	path := filepath.Join(root, "docs", "traceability.md")
	const stale = "stale but must not be overwritten\n"
	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := Check(cfg, scope, &out); err == nil || !strings.Contains(out.String(), "is stale") {
		t.Fatalf("stale output accepted: %v\n%s", err, out.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != stale {
		t.Fatalf("check-output rewrote stale file: %q", got)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report ProblemReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Summary.Integrity != 1 || report.Summary.Baselinable != 0 {
		t.Fatalf("output drift report = %+v", report)
	}
}

func TestCheckOutputDoesNotCreateMissingOutput(t *testing.T) {
	cfg := cfgT(t)
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, fixtureWaivers)
	path := filepath.Join(root, "docs", "traceability.md")
	var out strings.Builder
	err := Check(cfg, Scope{
		Root:        root,
		Catalog:     filepath.Join(root, "spec", "requirements.md"),
		Waivers:     filepath.Join(root, "spec", "waivers.md"),
		Out:         "docs/traceability.md",
		CheckOutput: true,
	}, &out)
	if err == nil || !strings.Contains(out.String(), "is missing") {
		t.Fatalf("missing generated output accepted: %v\n%s", err, out.String())
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("check-output created missing file, stat error = %v", statErr)
	}
}

func TestCheckOutputRunsThroughBaselinableStrictBacklog(t *testing.T) {
	cfg := cfgT(t)
	root := writeRepo(t, `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
`, "", "")
	scope := Scope{
		Root:         root,
		Catalog:      filepath.Join(root, "spec", "requirements.md"),
		Out:          "docs/traceability.md",
		ProblemsJSON: "docs/problems.json",
	}
	if err := Check(cfg, scope, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "docs", "traceability.md")); err != nil {
		t.Fatal(err)
	}
	scope.Strict = true
	scope.CheckOutput = true
	var out strings.Builder
	if err := Check(cfg, scope, &out); err == nil {
		t.Fatal("strict backlog with missing checked output unexpectedly passed")
	}
	if !strings.Contains(out.String(), "no tagged test or waiver") || !strings.Contains(out.String(), "generated output") || !strings.Contains(out.String(), "is missing") {
		t.Fatalf("requested output check was skipped through strict backlog:\n%s", out.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report ProblemReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Scope.CheckOutput || report.Scope.MatrixMarkdown != "docs/traceability.md" || report.Summary.Baselinable != 1 || report.Summary.Integrity != 1 {
		t.Fatalf("output-check scope/report = %+v", report)
	}
}

func TestCheckOutputReportsWhenIntegrityBlocksRendering(t *testing.T) {
	cfg := cfgT(t)
	root := writeRepo(t, fixtureCatalog+`
### REQ-CORE-001 — duplicate
- Section: §9
- Keyword: MUST
`, fixtureTestFile, fixtureWaivers)
	var out strings.Builder
	err := Check(cfg, Scope{
		Root:        root,
		Catalog:     filepath.Join(root, "spec", "requirements.md"),
		Waivers:     filepath.Join(root, "spec", "waivers.md"),
		Out:         "docs/traceability.md",
		CheckOutput: true,
	}, &out)
	if err == nil || !strings.Contains(out.String(), "verification skipped because traceability integrity problems") {
		t.Fatalf("unsafe output verification was silently skipped: %v\n%s", err, out.String())
	}
}

func TestProblemReportWriteFailurePreservesHumanDiagnostics(t *testing.T) {
	cfg := cfgT(t)
	root := writeRepo(t, `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
`, "", "")
	problemPath := filepath.Join(root, "docs", "problems.json")
	out := &mutatingWriter{mutate: func() {
		if err := os.Remove(problemPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(problemPath, 0o700); err != nil {
			t.Fatal(err)
		}
	}}
	err := Check(cfg, Scope{
		Root:         root,
		Catalog:      filepath.Join(root, "spec", "requirements.md"),
		Strict:       true,
		ProblemsJSON: "docs/problems.json",
	}, out)
	if err == nil || !strings.Contains(err.Error(), "additionally failed to write problems JSON") {
		t.Fatalf("problem report write failure not returned: %v", err)
	}
	if !strings.Contains(out.String(), "no tagged test or waiver") {
		t.Fatalf("human diagnostics suppressed: %s", out.String())
	}
}

func TestProblemReportLateSymlinkSwapCannotOverwriteCatalog(t *testing.T) {
	cfg := cfgT(t)
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, fixtureWaivers)
	catalogPath := filepath.Join(root, "spec", "requirements.md")
	catalogBefore, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	problemPath := filepath.Join(root, "docs", "problems.json")
	out := &mutatingWriter{mutate: func() {
		if err := os.Remove(problemPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(catalogPath, problemPath); err != nil {
			t.Fatal(err)
		}
	}}
	if err := Check(cfg, Scope{
		Root:         root,
		Catalog:      catalogPath,
		Waivers:      filepath.Join(root, "spec", "waivers.md"),
		ProblemsJSON: "docs/problems.json",
	}, out); err != nil {
		t.Fatal(err)
	}
	catalogAfter, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(catalogAfter) != string(catalogBefore) {
		t.Fatal("late report-path symlink swap overwrote the catalog")
	}
	info, err := os.Lstat(problemPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("final report followed or retained the injected symlink")
	}
	data, err := os.ReadFile(problemPath)
	if err != nil {
		t.Fatal(err)
	}
	var report ProblemReport
	if err := json.Unmarshal(data, &report); err != nil || !report.Complete {
		t.Fatalf("atomic final report = %+v, %v", report, err)
	}
}

func TestProblemReportParentSymlinkSwapCannotRedirectFinalWrite(t *testing.T) {
	cfg := cfgT(t)
	root := t.TempDir()
	catalogPath := filepath.Join(root, "spec", "report.json")
	mustWriteFile(t, root, "spec/report.json", fixtureCatalog)
	mustWriteFile(t, root, "pkg_test.go", fixtureTestFile)
	mustWriteFile(t, root, "spec/waivers.md", fixtureWaivers)
	catalogBefore, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	safeDir := filepath.Join(root, "safe-output")
	if err := os.Mkdir(safeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outputParent := filepath.Join(root, "out-link")
	if err := os.Symlink(safeDir, outputParent); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	out := &mutatingWriter{mutate: func() {
		if err := os.Remove(outputParent); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, "spec"), outputParent); err != nil {
			t.Fatal(err)
		}
	}}
	if err := Check(cfg, Scope{
		Root:         root,
		Catalog:      catalogPath,
		Waivers:      filepath.Join(root, "spec", "waivers.md"),
		ProblemsJSON: filepath.Join("out-link", "report.json"),
	}, out); err != nil {
		t.Fatal(err)
	}
	catalogAfter, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(catalogAfter) != string(catalogBefore) {
		t.Fatal("parent symlink swap redirected final report into the catalog")
	}
	data, err := os.ReadFile(filepath.Join(safeDir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report ProblemReport
	if err := json.Unmarshal(data, &report); err != nil || !report.Complete {
		t.Fatalf("descriptor-bound final report = %+v, %v", report, err)
	}
}

func TestProblemReportRelativePathsIgnoreWriterChdir(t *testing.T) {
	cfg := cfgT(t)
	root := writeRepo(t, fixtureCatalog, fixtureTestFile, fixtureWaivers)
	startingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(startingDir) })
	relRoot, err := filepath.Rel(startingDir, root)
	if err != nil {
		t.Fatal(err)
	}
	otherDir := t.TempDir()
	out := &mutatingWriter{mutate: func() {
		if err := os.Chdir(otherDir); err != nil {
			t.Fatal(err)
		}
	}}
	err = Check(cfg, Scope{
		Root:         relRoot,
		Catalog:      filepath.Join(relRoot, "spec", "requirements.md"),
		Waivers:      filepath.Join(relRoot, "spec", "waivers.md"),
		ProblemsJSON: "docs/problems.json",
	}, out)
	if restoreErr := os.Chdir(startingDir); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report ProblemReport
	if err := json.Unmarshal(data, &report); err != nil || !report.Complete || report.Artifacts.Catalog == nil || !report.Artifacts.Catalog.Present {
		t.Fatalf("cwd-stable report = %+v, %v", report, err)
	}
	if _, err := os.Stat(filepath.Join(otherDir, "docs", "problems.json")); !os.IsNotExist(err) {
		t.Fatalf("writer chdir redirected final report: %v", err)
	}
}

func TestGeneratedOutputReplacesSymlinkWithoutFollowingIt(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "catalog.md")
	const original = "control artifact\n"
	if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "matrix.md")
	if err := os.Symlink(target, output); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := writeOrCheckGenerated(output, []byte("generated\n"), false); err != nil {
		t.Fatal(err)
	}
	gotTarget, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotTarget) != original {
		t.Fatal("generated output followed a symlink into a control artifact")
	}
	gotOutput, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotOutput) != "generated\n" {
		t.Fatalf("generated output = %q", gotOutput)
	}
}

func TestGeneratedOutputPreservesExistingRegularFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "matrix.md")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeOrCheckGenerated(path, []byte("new\n"), false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("generated output mode = %o, want 644", got)
	}
}

type mutatingWriter struct {
	strings.Builder
	mutated bool
	mutate  func()
}

func (w *mutatingWriter) Write(p []byte) (int, error) {
	if !w.mutated {
		w.mutated = true
		w.mutate()
	}
	return w.Builder.Write(p)
}

func TestMultiColumnMatrixAndGroupBy(t *testing.T) {
	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{
		{Name: "Component", Required: true, Enum: []string{"a", "b"}},
	}
	cfg.Coverage.Rules = []CoverageRule{
		{Class: "conformance", FuncPrefixes: []string{"TestConf"}},
	}
	cfg.Matrix.CoverageColumns = []MatrixColumn{
		{Class: "unit", Label: "Unit"},
		{Class: "conformance", Label: "Conformance"},
	}
	cfg.Matrix.GroupBy = "Component"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	catalog := `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
- Component: a
### REQ-CORE-002
- Section: §1
- Keyword: MUST
- Component: b
`
	tests := `package p
import "testing"
// Verifies: REQ-CORE-001
func TestUnitA(t *testing.T) {}
// Verifies: REQ-CORE-001
func TestConfA(t *testing.T) {}
// Verifies: REQ-CORE-002
func TestUnitB(t *testing.T) {}
`
	// Wait - one test one ID is fine, two tests same ID fine.
	// TestConfA and TestUnitA both tag 001 - but tags.go errors if ONE test tags multiple IDs.
	// Two tests can tag same ID? Looking at CollectTags - each function can have one ID, multiple functions can map to same ID. Yes.

	// Actually TestUnitA and TestConfA both verify 001 - good.
	root := writeRepo(t, catalog, tests, "")
	var out strings.Builder
	err := Check(&cfg, Scope{
		Root:    root,
		Catalog: filepath.Join(root, "spec", "requirements.md"),
		Out:     "docs/traceability.md",
		Strict:  true,
	}, &out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	matrix, err := os.ReadFile(filepath.Join(root, "docs", "traceability.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(matrix)
	if !strings.Contains(s, "## Component: a") || !strings.Contains(s, "## Component: b") {
		t.Errorf("missing group headers: %s", s)
	}
	if !strings.Contains(s, "| Unit | Conformance |") {
		t.Errorf("missing multi columns: %s", s)
	}
}

func TestUnknownProfile(t *testing.T) {
	cfg := cfgT(t)
	scope := Scope{}
	if err := cfg.ApplyProfile("nope", &scope); err == nil {
		t.Fatal("expected error")
	}
}

// policyWaiverCfg is a config with one strict policy rule requiring
// conformance coverage on Kind: encoding requirements.
func policyWaiverCfg(t *testing.T) Config {
	t.Helper()
	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{
		{Name: "Kind", Required: true, Enum: []string{"encoding"}},
	}
	cfg.Coverage.Rules = []CoverageRule{
		{Class: "conformance", PathPrefixes: []string{"conformance/"}},
	}
	cfg.Policy.Rules = []PolicyRule{
		{When: map[string][]string{"Kind": {"encoding"}}, StrictRequiresCoverageClass: "conformance"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

const policyWaiverCatalog = `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
- Kind: encoding
`

func runPolicyWaiver(t *testing.T, cfg Config, waivers string) (error, string) {
	t.Helper()
	root := writeRepo(t, policyWaiverCatalog, "", waivers)
	var out strings.Builder
	err := Check(&cfg, Scope{
		Root:    root,
		Catalog: filepath.Join(root, "spec", "requirements.md"),
		Waivers: filepath.Join(root, "spec", "waivers.md"),
		Strict:  true,
	}, &out)
	return err, out.String()
}

func TestPolicyWaiverReasonsSatisfy(t *testing.T) {
	// A deliberate-excusal waiver satisfies the policy rule under strict.
	err, out := runPolicyWaiver(t, policyWaiverCfg(t), `# Waivers
### REQ-CORE-001
- Reason: documented-deviation
- Rationale: deviates per ADR-7.
`)
	if err != nil {
		t.Fatalf("documented-deviation should satisfy policy: %v\n%s", err, out)
	}

	// A not-implemented placeholder satisfies base coverage but NOT the rule.
	err, out = runPolicyWaiver(t, policyWaiverCfg(t), `# Waivers
### REQ-CORE-001
- Reason: not-implemented
- Rationale: later.
`)
	if err == nil || !strings.Contains(out, "policy requires conformance coverage") {
		t.Fatalf("not-implemented should not satisfy policy: %v\n%s", err, out)
	}
	if strings.Contains(out, "no tagged test or waiver") {
		t.Fatalf("waiver should still satisfy base strict coverage:\n%s", out)
	}

	// An explicit empty list disables waiver satisfaction entirely.
	cfg := policyWaiverCfg(t)
	cfg.Policy.WaiverReasonsSatisfy = []string{}
	err, out = runPolicyWaiver(t, cfg, `# Waivers
### REQ-CORE-001
- Reason: documented-deviation
- Rationale: deviates per ADR-7.
`)
	if err == nil || !strings.Contains(out, "policy requires conformance coverage") {
		t.Fatalf("empty waiverReasonsSatisfy should disable satisfaction: %v\n%s", err, out)
	}
}

func TestPolicyWaiverReasonsSatisfyValidation(t *testing.T) {
	cfg := Default()
	cfg.Policy.WaiverReasonsSatisfy = []string{"no-such-reason"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "no-such-reason") {
		t.Fatalf("want validation error naming the bad reason, got %v", err)
	}
}

func TestStrictWaiverReasonsSatisfyBaseCoverage(t *testing.T) {
	catalog := `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
`
	tests := []struct {
		name       string
		reasons    []string
		waiver     string
		wantErr    bool
		wantOutput string
	}{
		{
			name:    "omitted preserves legacy any-waiver behavior",
			reasons: nil,
			waiver: `# Waivers
### REQ-CORE-001
- Reason: not-implemented
- Rationale: legacy placeholder.
`,
		},
		{
			name:    "deliberate deviation accepted",
			reasons: []string{"covered-by", "documented-deviation"},
			waiver: `# Waivers
### REQ-CORE-001
- Reason: documented-deviation
- Rationale: replaced by an external control.
`,
		},
		{
			name:       "placeholder rejected",
			reasons:    []string{"covered-by", "documented-deviation"},
			wantErr:    true,
			wantOutput: `waiver reason "not-implemented" does not satisfy strict coverage`,
			waiver: `# Waivers
### REQ-CORE-001
- Reason: not-implemented
- Rationale: later.
`,
		},
		{
			name:       "explicit empty list rejects every waiver",
			reasons:    []string{},
			wantErr:    true,
			wantOutput: `waiver reason "documented-deviation" does not satisfy strict coverage`,
			waiver: `# Waivers
### REQ-CORE-001
- Reason: documented-deviation
- Rationale: no waiver is accepted by this profile.
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Strict.WaiverReasonsSatisfy = tt.reasons
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			root := writeRepo(t, catalog, "", tt.waiver)
			var out strings.Builder
			err := Check(&cfg, Scope{
				Root:    root,
				Catalog: filepath.Join(root, "spec", "requirements.md"),
				Waivers: filepath.Join(root, "spec", "waivers.md"),
				Strict:  true,
			}, &out)
			if tt.wantErr {
				if err == nil || !strings.Contains(out.String(), tt.wantOutput) {
					t.Fatalf("strict waiver unexpectedly accepted: %v\n%s", err, out.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("strict waiver unexpectedly rejected: %v\n%s", err, out.String())
			}
		})
	}
}

func TestStrictCoveredByWaiverNeedsTaggedTarget(t *testing.T) {
	cfg := Default()
	cfg.Waivers.RequireCoversForCoveredBy = true
	cfg.Strict.WaiverReasonsSatisfy = []string{"covered-by"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	catalog := `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST

### REQ-CORE-002
- Section: §1
- Keyword: MUST
`
	waivers := `# Waivers
### REQ-CORE-001
- Reason: covered-by
- Covers: REQ-CORE-002
- Rationale: exact composite.

### REQ-CORE-002
- Reason: not-implemented
- Rationale: target has no evidence yet.
`
	root := writeRepo(t, catalog, "", waivers)
	var out strings.Builder
	err := Check(&cfg, Scope{
		Root:    root,
		Catalog: filepath.Join(root, "spec", "requirements.md"),
		Waivers: filepath.Join(root, "spec", "waivers.md"),
		Strict:  true,
	}, &out)
	if err == nil || !strings.Contains(out.String(), "covered-by target REQ-CORE-002 has no tagged test for strict coverage") {
		t.Fatalf("evidence-free covered-by accepted: %v\n%s", err, out.String())
	}

	mustWriteFile(t, root, "pkg_test.go", `package p
import "testing"
// Verifies: REQ-CORE-002
func TestTarget(t *testing.T) {}
`)
	waivers = `# Waivers
### REQ-CORE-001
- Reason: covered-by
- Covers: REQ-CORE-002
- Rationale: exact composite.
`
	mustWriteFile(t, root, "spec/waivers.md", waivers)
	out.Reset()
	if err := Check(&cfg, Scope{
		Root:    root,
		Catalog: filepath.Join(root, "spec", "requirements.md"),
		Waivers: filepath.Join(root, "spec", "waivers.md"),
		Strict:  true,
	}, &out); err != nil {
		t.Fatalf("tag-backed covered-by rejected: %v\n%s", err, out.String())
	}
}

func TestStrictWaiverReasonsSatisfyValidation(t *testing.T) {
	cfg := Default()
	cfg.Strict.WaiverReasonsSatisfy = []string{"no-such-reason"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "no-such-reason") {
		t.Fatalf("unknown strict waiver reason accepted: %v", err)
	}
}

func TestClassificationStrictWaiverSatisfies(t *testing.T) {
	// The same waiver semantics apply to classification strictRequires.
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	catalog := `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
`
	classification := `# Classification
### REQ-CORE-001
- Class: wire-observable
`
	waivers := `# Waivers
### REQ-CORE-001
- Reason: documented-deviation
- Rationale: deviates per ADR-7.
`
	root := writeRepo(t, catalog, "", waivers)
	mustWriteFile(t, root, "spec/classification.md", classification)
	var out strings.Builder
	err := Check(&cfg, Scope{
		Root:           root,
		Catalog:        filepath.Join(root, "spec", "requirements.md"),
		Classification: filepath.Join(root, "spec", "classification.md"),
		Waivers:        filepath.Join(root, "spec", "waivers.md"),
		Strict:         true,
	}, &out)
	if err != nil {
		t.Fatalf("documented-deviation should satisfy classification strict class: %v\n%s", err, out.String())
	}
}

func TestPolicyCoveredByWaiverNeedsRealCoverage(t *testing.T) {
	catalog := `# Catalog
### REQ-CORE-001
- Section: §1
- Keyword: MUST
- Kind: encoding
### REQ-CORE-002
- Section: §1
- Keyword: MUST
- Kind: encoding
`
	waivers := `# Waivers
### REQ-CORE-002
- Reason: covered-by
- Covers: REQ-CORE-001
- Rationale: special case of 001.
`
	run := func(t *testing.T, conformanceTag bool) (error, string) {
		t.Helper()
		cfg := policyWaiverCfg(t)
		root := writeRepo(t, catalog, "", waivers)
		mustWriteFile(t, root, "unit_test.go",
			"package p\nimport \"testing\"\n// Verifies: REQ-CORE-001\nfunc TestUnit(t *testing.T) {}\n")
		if conformanceTag {
			mustWriteFile(t, root, "conformance/enc_test.go",
				"package conformance\nimport \"testing\"\n// Verifies: REQ-CORE-001\nfunc TestEnc(t *testing.T) {}\n")
		}
		var out strings.Builder
		err := Check(&cfg, Scope{
			Root:    root,
			Catalog: filepath.Join(root, "spec", "requirements.md"),
			Waivers: filepath.Join(root, "spec", "waivers.md"),
			Strict:  true,
		}, &out)
		return err, out.String()
	}

	// Target has only unit coverage: the covered-by waiver must NOT satisfy
	// the conformance policy rule for 002 (and 001 itself also fails).
	err, out := run(t, false)
	if err == nil || !strings.Contains(out, "REQ-CORE-002 (§1): policy requires conformance coverage") {
		t.Fatalf("covered-by without target conformance coverage should not satisfy: %v\n%s", err, out)
	}

	// Target carries a conformance tag: both 001 (tagged) and 002 (covered-by
	// proxy to a conformance-covered target) pass.
	err, out = run(t, true)
	if err != nil {
		t.Fatalf("covered-by with conformance-covered target should satisfy: %v\n%s", err, out)
	}
}
