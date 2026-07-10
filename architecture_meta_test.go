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
