package tracecheck

import (
	"os"
	"path/filepath"
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
