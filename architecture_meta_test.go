package tracecheck

import (
	"encoding/json"
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
	if mj.Requirements[0].Meta["Phase"] != "1" && mj.Requirements[1].Meta["Phase"] != "1" {
		t.Fatalf("meta not in json: %+v", mj.Requirements)
	}
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
