package tracecheck

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfigValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Default() must validate, got: %v", err)
	}
}

func TestConfigRejectsBadIDPattern(t *testing.T) {
	cfg := Default()
	cfg.IDGrammar.Pattern = "REQ-(" // unbalanced group
	if err := cfg.Validate(); err == nil {
		t.Fatal("bad ID pattern accepted")
	}
}

func TestConfigRejectsEmptyTagKeyword(t *testing.T) {
	cfg := Default()
	cfg.Tag.Keyword = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("empty tag keyword accepted")
	}
}

func TestConfigRejectsUnknownCollectorLang(t *testing.T) {
	cfg := Default()
	cfg.Tag.Collectors = []CollectorSpec{{Lang: "cobol"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("unknown collector lang accepted")
	}
}

func TestConfigRejectsCommentCollectorWithoutSuffix(t *testing.T) {
	cfg := Default()
	cfg.Tag.Collectors = []CollectorSpec{{Lang: "comment"}} // no fileSuffix
	if err := cfg.Validate(); err == nil {
		t.Fatal("comment collector without fileSuffix accepted")
	}
}

func TestConfigRejectsBadSeriesPattern(t *testing.T) {
	cfg := Default()
	cfg.IDGrammar.SeriesPattern = "REQ-(" // unbalanced
	if err := cfg.Validate(); err == nil {
		t.Fatal("bad series pattern accepted")
	}
}

func TestConfigRejectsBadHeadingCandidatePattern(t *testing.T) {
	cfg := Default()
	cfg.IDGrammar.HeadingPrefix = ""
	cfg.IDGrammar.HeadingCandidatePattern = "["
	if err := cfg.Validate(); err == nil {
		t.Fatal("bad heading candidate pattern accepted")
	}
}

func TestHeadingCandidatePatternDetectsMalformedMultiSeriesHeading(t *testing.T) {
	cfg := Default()
	cfg.IDGrammar.Pattern = `[A-Z][A-Z0-9]*(?:-[A-Z][A-Z0-9]*)*-[0-9]+`
	cfg.IDGrammar.HeadingPrefix = ""
	cfg.IDGrammar.HeadingCandidatePattern = `^[A-Z][A-Z0-9-]*-`
	cfg.IDGrammar.SeriesPattern = ""
	cfg.IDGrammar.Subtypes = []Subtype{}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	catalog := `# Catalog

### TERRAPIN-2 — valid
- Section: §2
- Keyword: MUST

### ENC-BD-X — malformed
- Section: §15
- Keyword: MUST

### Notes

Narrative heading that is not ID-shaped.
`
	root := writeRepo(t, catalog, "", "")
	reqs, problems, err := ParseCatalog(&cfg, filepath.Join(root, "spec", "requirements.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].ID != "TERRAPIN-2" {
		t.Fatalf("requirements = %+v, want only TERRAPIN-2", reqs)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "ENC-BD-X") {
		t.Fatalf("malformed multi-series heading not reported: %v", problems)
	}
}

func TestConfigRejectsUnknownStrictPhaseField(t *testing.T) {
	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{{Name: "Phase", Enum: []string{"1", "2"}}}
	cfg.Strict.PhaseField = "Phaes"
	cfg.Strict.Phases = []string{"1"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), `strict.phaseField: unknown field "Phaes"`) {
		t.Fatalf("unknown strict phase field accepted: %v", err)
	}
}

func TestConfigRejectsUnknownStrictPhaseValue(t *testing.T) {
	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{{Name: "Phase", Enum: []string{"1", "2"}}}
	cfg.Strict.Phases = []string{"I"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), `strict.phases: unknown Phase value "I"`) {
		t.Fatalf("unknown strict phase value accepted: %v", err)
	}
}

func TestConfigRejectsUnknownStrictKeywordClass(t *testing.T) {
	cfg := Default()
	cfg.Strict.KeywordClasses = []string{"msut"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), `strict.keywordClasses: unknown keyword class "msut"`) {
		t.Fatalf("unknown strict keyword class accepted: %v", err)
	}
}

func TestConfigRejectsUnknownProfileFilters(t *testing.T) {
	tests := []struct {
		name    string
		profile Profile
		want    string
	}{
		{name: "phase", profile: Profile{Strict: true, StrictPhases: []string{"I"}}, want: `unknown Phase value "I"`},
		{name: "class", profile: Profile{Strict: true, StrictKeywordClasses: []string{"msut"}}, want: `unknown keyword class "msut"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Catalog.Fields = []CatalogField{{Name: "Phase", Enum: []string{"1", "2"}}}
			cfg.Profiles = map[string]Profile{"freeze": tt.profile}
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("unknown profile filter accepted: %v", err)
			}
		})
	}
}

func TestCheckRejectsUnknownScopeFilters(t *testing.T) {
	tests := []struct {
		name  string
		scope Scope
		want  string
	}{
		{name: "phase", scope: Scope{Strict: true, StrictPhases: []string{"I"}}, want: `unknown Phase value "I"`},
		{name: "class", scope: Scope{Strict: true, StrictKeywordClasses: []string{"msut"}}, want: `unknown keyword class "msut"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Catalog.Fields = []CatalogField{{Name: "Phase", Enum: []string{"1", "2"}}}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			root := writeRepo(t, fixtureCatalog, "", "")
			tt.scope.Root = root
			tt.scope.Catalog = filepath.Join(root, "spec", "requirements.md")
			if err := Check(&cfg, tt.scope, io.Discard); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("unknown scope filter accepted: %v", err)
			}
		})
	}
}

func TestValidateScopeRejectsOutputPathCollisions(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	input := filepath.Join(root, "same.md")
	tests := []Scope{
		{Root: root, Out: "same.json", OutJSON: "same.json"},
		{Root: root, Out: "same.json", ProblemsJSON: "same.json"},
		{Root: root, OutJSON: "same.json", ProblemsJSON: "same.json"},
		{Root: root, Out: "same.md", Catalog: input},
		{Root: root, ProblemsJSON: "same.md", Waivers: input},
		{Root: root, OutJSON: "same.md", Classification: input},
		{Root: root, Out: "same.md", ConfigPath: input},
	}
	for _, scope := range tests {
		if err := cfg.ValidateScope(scope); err == nil || !strings.Contains(err.Error(), "path collision") {
			t.Errorf("colliding scope accepted: %+v: %v", scope, err)
		}
	}
}

func TestValidateScopeRejectsCollisionWithCLIShapedRelativeRoot(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	absRoot := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relRoot, err := filepath.Rel(cwd, absRoot)
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{
		Root:    relRoot,
		Catalog: filepath.Join(relRoot, "spec", "requirements.md"),
		Out:     "spec/requirements.md",
	}
	if err := cfg.ValidateScope(scope); err == nil || !strings.Contains(err.Error(), "output/input path collision") {
		t.Fatalf("CLI-shaped relative-root collision accepted: %v", err)
	}
}

func TestValidateScopeRejectsOutputAliasesToInputs(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	input := filepath.Join(root, "catalog.md")
	if err := os.WriteFile(input, []byte("catalog\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("symlink", func(t *testing.T) {
		output := filepath.Join(root, "matrix-symlink.md")
		if err := os.Symlink(input, output); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := cfg.ValidateScope(Scope{Root: root, Catalog: input, Out: filepath.Base(output)}); err == nil || !strings.Contains(err.Error(), "path collision") {
			t.Fatalf("symlinked output/input collision accepted: %v", err)
		}
	})
	t.Run("hardlink", func(t *testing.T) {
		output := filepath.Join(root, "matrix-hardlink.md")
		if err := os.Link(input, output); err != nil {
			t.Skipf("hardlink unavailable: %v", err)
		}
		if err := cfg.ValidateScope(Scope{Root: root, Catalog: input, Out: filepath.Base(output)}); err == nil || !strings.Contains(err.Error(), "path collision") {
			t.Fatalf("hardlinked output/input collision accepted: %v", err)
		}
	})
	t.Run("symlinked parents with missing target", func(t *testing.T) {
		target := filepath.Join(root, "output-target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		left := filepath.Join(root, "left")
		right := filepath.Join(root, "right")
		if err := os.Symlink(target, left); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := os.Symlink(target, right); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		scope := Scope{
			Root:    root,
			Out:     filepath.Join("left", "same.json"),
			OutJSON: filepath.Join("right", "same.json"),
		}
		if err := cfg.ValidateScope(scope); err == nil || !strings.Contains(err.Error(), "output path collision") {
			t.Fatalf("symlinked-parent output collision accepted: %v", err)
		}
	})
	t.Run("dangling final symlink", func(t *testing.T) {
		matrix := filepath.Join(root, "matrix.md")
		alias := filepath.Join(root, "matrix-alias.md")
		if err := os.Symlink(filepath.Base(matrix), alias); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		scope := Scope{Root: root, Out: filepath.Base(matrix), OutJSON: filepath.Base(alias)}
		if err := cfg.ValidateScope(scope); err == nil || !strings.Contains(err.Error(), "output path collision") {
			t.Fatalf("dangling-symlink output collision accepted: %v", err)
		}
	})
	t.Run("dangling output aliases missing optional input", func(t *testing.T) {
		classification := filepath.Join(root, "missing-classification.md")
		problemsLink := filepath.Join(root, "problems-link.json")
		if err := os.Symlink(filepath.Base(classification), problemsLink); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		scope := Scope{
			Root:           root,
			Classification: classification,
			ProblemsJSON:   filepath.Base(problemsLink),
		}
		if err := cfg.ValidateScope(scope); err == nil || !strings.Contains(err.Error(), "output/input path collision") {
			t.Fatalf("dangling output alias to missing input accepted: %v", err)
		}
	})
}

func TestValidateScopeBindsLoadedConfigSourcePath(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "tracecheck.json")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "other.json")
	if err := cfg.ValidateScope(Scope{Root: root, ConfigPath: other}); err == nil || !strings.Contains(err.Error(), "does not match loaded config source") {
		t.Fatalf("mismatched config source accepted: %v", err)
	}
	if err := cfg.ValidateScope(Scope{Root: root, Out: "tracecheck.json"}); err == nil || !strings.Contains(err.Error(), "output/input path collision") {
		t.Fatalf("output collision with implicit loaded config source accepted: %v", err)
	}
}

func TestValidateScopeRejectsCaseAliasedOutputsOnCaseInsensitiveFilesystem(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	probe := filepath.Join(root, "CaseProbe")
	if err := os.WriteFile(probe, []byte("probe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "caseprobe")); os.IsNotExist(err) {
		t.Skip("filesystem is case-sensitive")
	} else if err != nil {
		t.Fatal(err)
	}
	scope := Scope{Root: root, Out: "same", OutJSON: "SAME"}
	if err := cfg.ValidateScope(scope); err == nil || !strings.Contains(err.Error(), "output path collision") {
		t.Fatalf("case-aliased outputs accepted: %v", err)
	}
	scope = Scope{Root: root, Out: filepath.Join("MissingDir", "same"), OutJSON: filepath.Join("MissingDir", "SAME")}
	if err := cfg.ValidateScope(scope); err == nil || !strings.Contains(err.Error(), "output path collision") {
		t.Fatalf("nested case-aliased outputs accepted: %v", err)
	}
}

func TestConfigRejectsUnknownPolicyMetadataField(t *testing.T) {
	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{{Name: "Kind", Enum: []string{"encoding", "ops"}}}
	cfg.Policy.Rules = []PolicyRule{{
		When:                        map[string][]string{"Knd": {"encoding"}},
		StrictRequiresCoverageClass: "unit",
	}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), `unknown field "Knd"`) {
		t.Fatalf("unknown policy field accepted: %v", err)
	}
}

func TestConfigRejectsUnknownPolicyMetadataValue(t *testing.T) {
	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{{Name: "Kind", Enum: []string{"encoding", "ops"}}}
	cfg.Policy.Rules = []PolicyRule{{
		When:                        map[string][]string{"Kind": {"encodng"}},
		StrictRequiresCoverageClass: "unit",
	}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), `unknown Kind value "encodng"`) {
		t.Fatalf("unknown policy value accepted: %v", err)
	}
}

func TestConfigRejectsUnknownPolicyCoverageClass(t *testing.T) {
	cfg := Default()
	cfg.Policy.Rules = []PolicyRule{{StrictRequiresCoverageClass: "integrtion"}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), `unknown coverage class "integrtion"`) {
		t.Fatalf("unknown policy coverage class accepted: %v", err)
	}
}

func TestConfigRejectsUnknownMatrixGroupField(t *testing.T) {
	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{{Name: "Component", Enum: []string{"cas"}}}
	cfg.Matrix.GroupBy = "Componet"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), `matrix.groupBy: unknown catalog field "Componet"`) {
		t.Fatalf("unknown matrix group field accepted: %v", err)
	}
}

func TestConfigRejectsContradictoryPolicyEffects(t *testing.T) {
	tests := []struct {
		name string
		rule PolicyRule
		want string
	}{
		{
			name: "allow and require",
			rule: PolicyRule{AllowUncovered: true, StrictRequiresCoverageClass: "unit"},
			want: "allowUncovered cannot be combined with strictRequiresCoverageClass",
		},
		{
			name: "require and forbid same class",
			rule: PolicyRule{StrictRequiresCoverageClass: "unit", ForbidsCoverageClass: "unit"},
			want: "requires and forbids coverage class",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Policy.Rules = []PolicyRule{tt.rule}
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("contradictory policy accepted: %v", err)
			}
		})
	}
}

func TestConfigRejectsCatalogFieldWhitespace(t *testing.T) {
	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{{Name: " Kind", Enum: []string{"encoding"}}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "leading or trailing whitespace") {
		t.Fatalf("non-canonical catalog field accepted: %v", err)
	}
}

// TestLoadConfigOverlaysDefaults: a partial JSON config overrides only the
// fields it names; everything else keeps the documented defaults.
func TestLoadConfigOverlaysDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tracecheck.json")
	if err := os.WriteFile(path, []byte(`{"tag":{"keyword":"Covers:"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tag.Keyword != "Covers:" {
		t.Errorf("tag keyword = %q, want overridden Covers:", cfg.Tag.Keyword)
	}
	// IDGrammar was not named in the JSON, so it keeps the default.
	if cfg.IDGrammar.Pattern != Default().IDGrammar.Pattern {
		t.Errorf("IDGrammar.Pattern = %q, want default", cfg.IDGrammar.Pattern)
	}
}

// TestLoadConfigReplacesStructSlicesNoLeak: a JSON array of structs must fully
// replace the default, not merge field-by-field into the default's elements.
// (Go's json decoder reuses backing slice elements, which would leak default
// fields like StrictRequiresCoverageClass into a shorter/partial array.)
func TestLoadConfigReplacesStructSlicesNoLeak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tracecheck.json")
	body := `{"classification":{"values":[{"name":"checked"},{"name":"unchecked","requiresReason":true}]}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Classification.Values) != 2 {
		t.Fatalf("values = %+v, want exactly 2", cfg.Classification.Values)
	}
	for _, v := range cfg.Classification.Values {
		if v.StrictRequiresCoverageClass != "" || v.ForbidsCoverageClass != "" {
			t.Errorf("value %q leaked default coverage-class fields: %+v", v.Name, v)
		}
	}
}

// TestLoadConfigExplicitEmptySliceKept: an explicit empty array removes a
// default (e.g. subtypes:[] drops IMP/DEC), distinct from omitting the key.
func TestLoadConfigExplicitEmptySliceKept(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tracecheck.json")
	if err := os.WriteFile(path, []byte(`{"idGrammar":{"subtypes":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.IDGrammar.Subtypes) != 0 {
		t.Errorf("explicit empty subtypes not honored: %+v", cfg.IDGrammar.Subtypes)
	}
	// An omitted slice still keeps its default.
	if len(cfg.KeywordClasses) != len(Default().KeywordClasses) {
		t.Errorf("omitted keywordClasses lost its default: %+v", cfg.KeywordClasses)
	}
}

func TestLoadConfigStrictWaiverReasonOmittedVsEmpty(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantNil bool
		wantLen int
	}{
		{name: "omitted", body: `{}`, wantNil: true},
		{name: "explicit empty", body: `{"strict":{"waiverReasonsSatisfy":[]}}`, wantLen: 0},
		{name: "allowlist", body: `{"strict":{"waiverReasonsSatisfy":["documented-deviation"]}}`, wantLen: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tracecheck.json")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantNil != (cfg.Strict.WaiverReasonsSatisfy == nil) || len(cfg.Strict.WaiverReasonsSatisfy) != tt.wantLen {
				t.Fatalf("strict waiver reasons = %#v, want nil=%t len=%d", cfg.Strict.WaiverReasonsSatisfy, tt.wantNil, tt.wantLen)
			}
		})
	}
}

func TestLoadConfigRejectsBadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("malformed JSON accepted")
	}
}

func TestLoadConfigRejectsTrailingContent(t *testing.T) {
	for _, body := range []string{
		"{}\n{}\n",
		"{}\nGARBAGE\n",
	} {
		path := filepath.Join(t.TempDir(), "tracecheck.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatalf("config with trailing content accepted: %q", body)
		}
	}
}

// TestDefaultSeriesOf: the default series pattern extracts the series segment
// the way the original hardcoded seriesOf did.
func TestDefaultSeriesOf(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"REQ-CORE-001":     "CORE",
		"REQ-CORE-DEC-001": "CORE",
		"REQ-API-042":      "API",
		"not-an-id":        "",
	}
	for id, want := range cases {
		if got := cfg.seriesOf(id); got != want {
			t.Errorf("seriesOf(%q) = %q, want %q", id, got, want)
		}
	}
}

// TestDefaultClassFromKeyword: the default keyword classes reproduce the
// original classFromKeyword mapping, strongest class winning.
func TestDefaultClassFromKeyword(t *testing.T) {
	cfg := Default()
	cases := map[string]string{
		"MUST":        "must",
		"MUST NOT":    "must",
		"SHALL":       "must",
		"REQUIRED":    "must",
		"RECOMMENDED": "should",
		"SHOULD":      "should",
		"MAY":         "may",
		"OPTIONAL":    "may",
		"narrative":   "",
	}
	for kw, want := range cases {
		if got := cfg.classFromKeyword(kw); got != want {
			t.Errorf("classFromKeyword(%q) = %q, want %q", kw, got, want)
		}
	}
}

// TestDefaultClassFromID: IMP/DEC subtypes classify by ID like the original.
func TestDefaultClassFromID(t *testing.T) {
	cfg := Default()
	if got := cfg.classFromID("REQ-CORE-IMP-001"); got != "implicit" {
		t.Errorf("classFromID(IMP) = %q, want implicit", got)
	}
	if got := cfg.classFromID("REQ-CORE-DEC-001"); got != "decision" {
		t.Errorf("classFromID(DEC) = %q, want decision", got)
	}
	if got := cfg.classFromID("REQ-CORE-001"); got != "" {
		t.Errorf("classFromID(plain) = %q, want empty", got)
	}
}

func TestRequiredReasonsContainConventionalSet(t *testing.T) {
	cfg := Default()
	for _, r := range []string{"deployment-guidance", "not-implemented", "covered-by", "documented-deviation", "foundational"} {
		if !contains(cfg.Waivers.Reasons, r) {
			t.Errorf("default waiver reasons missing %q", r)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
