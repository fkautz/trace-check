package tracecheck

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Config describes a project's traceability dialect: how requirement IDs are
// shaped, how the catalog/waivers/classification markdown is laid out, what
// tag keyword marks coverage, which languages to scan, how coverage classes
// are assigned, and how the generated matrix is labelled. Every field that the
// original project-specific trace-check hardcoded lives here, with
// Default() reproducing that original behaviour so a project that wants the
// built-in dialect needs no config at all.
//
// Load a project config with LoadConfig; it overlays the JSON onto Default(),
// so a config file names only the fields it changes. Always Validate() before
// use — it compiles the regexes and rejects contradictory settings.
type Config struct {
	IDGrammar      IDGrammar      `json:"idGrammar"`
	Catalog        CatalogConfig  `json:"catalog"`
	KeywordClasses []KeywordClass `json:"keywordClasses"`
	Tag            TagConfig      `json:"tag"`
	Coverage       CoverageConfig `json:"coverage"`
	Waivers        WaiverConfig   `json:"waivers"`
	Classification ClassConfig    `json:"classification"`
	Matrix         MatrixConfig   `json:"matrix"`
	SkipDirs       []string       `json:"skipDirs"`

	compiled compiledConfig
}

// IDGrammar defines the requirement-ID shape and how to derive a series (the
// scope key) and subtype class from an ID.
type IDGrammar struct {
	// Pattern is the inner ID regex (no anchors), e.g.
	// REQ-[A-Z][A-Z0-9]*-(?:IMP-|DEC-)?\d{3}. It is anchored for full-ID matching
	// and embedded in the markdown heading regexes.
	Pattern string `json:"pattern"`
	// HeadingPrefix detects "this line meant to be a requirement heading":
	// an "### <HeadingPrefix>..." line that does not match Pattern is reported
	// as malformed rather than silently skipped.
	HeadingPrefix string `json:"headingPrefix"`
	// SeriesPattern's first capture group is the series segment used to scope
	// tag checking (so a core run ignores another series' tags). Empty
	// disables scoping (all tags share one series).
	SeriesPattern string `json:"seriesPattern"`
	// Subtypes classify IDs that carry no Keyword line (e.g. -IMP- implicit,
	// -DEC- decision). Checked in order; first contained marker wins.
	Subtypes []Subtype `json:"subtypes"`
}

// Subtype maps an ID-substring marker to a policy class.
type Subtype struct {
	Marker string `json:"marker"`
	Class  string `json:"class"`
}

// CatalogConfig names the markdown fields the catalog parser reads.
type CatalogConfig struct {
	KeywordField string `json:"keywordField"`
	SectionField string `json:"sectionField"`
	// SectionRefPattern extracts a canonical section reference from the
	// Section line (e.g. §[\d.]*\d). Empty falls back to the text up to the
	// first ";".
	SectionRefPattern string `json:"sectionRefPattern"`
}

// KeywordClass maps catalog Keyword tokens to a policy class. Entries are
// checked in order, so list the strongest class first (must before should).
type KeywordClass struct {
	Class    string   `json:"class"`
	Keywords []string `json:"keywords"`
}

// TagConfig defines the coverage-tag syntax and the per-language collectors.
type TagConfig struct {
	// Keyword introduces a tag line, e.g. "Verifies:". After stripping leading
	// comment markers and whitespace, the keyword must be at position 0.
	Keyword string `json:"keyword"`
	// CommentMarkers are stripped from the front of a source line before
	// looking for Keyword (e.g. //, ///, /*, */, *, #).
	CommentMarkers []string        `json:"commentMarkers"`
	Collectors     []CollectorSpec `json:"collectors"`
}

// CollectorSpec selects and configures one tag collector. Lang "go" uses the
// Go AST collector; "comment" uses the language-agnostic comment scanner.
type CollectorSpec struct {
	Lang string `json:"lang"`
	// FuncPrefixes are the names that mark a test (Go: Test, Fuzz, Example).
	FuncPrefixes []string `json:"funcPrefixes"`
	// FileSuffix (comment collector) selects files to scan, e.g. ".rs".
	FileSuffix string `json:"fileSuffix"`
	// TestMarkers (comment collector) are line prefixes that begin a test
	// item, e.g. "#[test]", "#[tokio::test]", "fn test_". A tag comment is
	// attributed to the nearest following test marker.
	TestMarkers []string `json:"testMarkers"`
	// NamePattern (comment collector) extracts a test name from the marker
	// line for display; first capture group is the name. Optional.
	NamePattern string `json:"namePattern"`
}

// CoverageConfig assigns a coverage class to each discovered tag.
type CoverageConfig struct {
	Default string         `json:"default"`
	Rules   []CoverageRule `json:"rules"`
}

// CoverageRule assigns Class to a tag whose file matches one of PathPrefixes
// (if any) AND whose test name matches one of FuncPrefixes (if any). An empty
// list means "no constraint on that dimension". Rules are checked in order.
type CoverageRule struct {
	Class        string   `json:"class"`
	PathPrefixes []string `json:"pathPrefixes"`
	FuncPrefixes []string `json:"funcPrefixes"`
}

// WaiverConfig names the waiver markdown fields and the allowed reasons.
type WaiverConfig struct {
	ReasonField    string   `json:"reasonField"`
	RationaleField string   `json:"rationaleField"`
	Reasons        []string `json:"reasons"`
}

// ClassConfig defines the wire-observability (or analogous) classification.
// The feature is dormant unless a classification file is present, matching the
// original file-presence semantics.
type ClassConfig struct {
	ClassField  string       `json:"classField"`
	ReasonField string       `json:"reasonField"`
	Values      []ClassValue `json:"values"`
}

// ClassValue is one classification value and the rules it imposes.
type ClassValue struct {
	Name           string `json:"name"`
	RequiresReason bool   `json:"requiresReason"`
	// ForbidsCoverageClass: a tag of this coverage class on a requirement with
	// this classification is a stale classification (e.g. a not-observable
	// requirement carrying a black-box scenario).
	ForbidsCoverageClass string `json:"forbidsCoverageClass"`
	// StrictRequiresCoverageClass: under -strict, a requirement with this
	// classification must carry a tag of this coverage class (e.g. a
	// wire-observable requirement needs a black-box scenario).
	StrictRequiresCoverageClass string `json:"strictRequiresCoverageClass"`
}

// MatrixConfig labels the two coverage columns of the generated matrix. A tag
// whose class equals SecondaryClass lands in the secondary column; every other
// class lands in the primary column.
type MatrixConfig struct {
	PrimaryClass       string `json:"primaryClass"`
	PrimaryLabel       string `json:"primaryLabel"`
	SecondaryClass     string `json:"secondaryClass"`
	SecondaryLabel     string `json:"secondaryLabel"`
	BothLabel          string `json:"bothLabel"`
	PrimaryOnlyLabel   string `json:"primaryOnlyLabel"`
	SecondaryOnlyLabel string `json:"secondaryOnlyLabel"`
	// GeneratedBy is the tool attribution in the matrix's "Generated by …"
	// line (markdown, so backticks are conventional).
	GeneratedBy string `json:"generatedBy"`
}

// compiledConfig holds the regexes built from Config during Validate.
type compiledConfig struct {
	fullID            *regexp.Regexp
	catalogHeading    *regexp.Regexp
	plainHeading      *regexp.Regexp // headings with no title (waivers/classification)
	looseHeading      *regexp.Regexp
	keywordLine       *regexp.Regexp
	sectionLine       *regexp.Regexp
	sectionRef        *regexp.Regexp // nil if SectionRefPattern is empty
	series            *regexp.Regexp // nil if SeriesPattern is empty
	reasonField       *regexp.Regexp // waiver reason line
	rationalePrefix   string
	classClassField   string
	classReasonPrefix string
}

// Default returns the configuration that reproduces the original
// hardcoded trace-check behaviour exactly. It is the documented baseline that
// LoadConfig overlays a project's JSON onto.
func Default() Config {
	return Config{
		IDGrammar: IDGrammar{
			Pattern:       `REQ-[A-Z][A-Z0-9]*-(?:IMP-|DEC-)?\d{3}`,
			HeadingPrefix: "REQ-",
			SeriesPattern: `^REQ-([A-Za-z0-9]+)-`,
			Subtypes: []Subtype{
				{Marker: "-IMP-", Class: "implicit"},
				{Marker: "-DEC-", Class: "decision"},
			},
		},
		Catalog: CatalogConfig{
			KeywordField:      "Keyword",
			SectionField:      "Section",
			SectionRefPattern: `§[\d.]*\d`,
		},
		KeywordClasses: []KeywordClass{
			{Class: "must", Keywords: []string{"MUST", "SHALL", "REQUIRED"}},
			{Class: "should", Keywords: []string{"SHOULD", "RECOMMENDED"}},
			{Class: "may", Keywords: []string{"MAY", "OPTIONAL"}},
		},
		Tag: TagConfig{
			Keyword:        "Verifies:",
			CommentMarkers: []string{"//", "/*", "*/"},
			Collectors: []CollectorSpec{
				{Lang: "go", FuncPrefixes: []string{"Test", "Fuzz", "Example"}},
			},
		},
		Coverage: CoverageConfig{
			Default: "unit",
			Rules: []CoverageRule{
				{Class: "blackbox", PathPrefixes: []string{"compliance/"}, FuncPrefixes: []string{"TestScenario", "TestSmoke"}},
			},
		},
		Waivers: WaiverConfig{
			ReasonField:    "Reason",
			RationaleField: "Rationale",
			Reasons:        []string{"deployment-guidance", "not-implemented", "covered-by", "documented-deviation", "foundational"},
		},
		Classification: ClassConfig{
			ClassField:  "Class",
			ReasonField: "Reason",
			Values: []ClassValue{
				{Name: "wire-observable", StrictRequiresCoverageClass: "blackbox"},
				{Name: "not-observable", RequiresReason: true, ForbidsCoverageClass: "blackbox"},
			},
		},
		Matrix: MatrixConfig{
			PrimaryClass:       "unit",
			PrimaryLabel:       "Unit coverage",
			SecondaryClass:     "blackbox",
			SecondaryLabel:     "Black-box coverage",
			BothLabel:          "both (unit + black-box)",
			PrimaryOnlyLabel:   "unit only",
			SecondaryOnlyLabel: "black-box only",
			GeneratedBy:        "`trace-check`",
		},
		SkipDirs: []string{".git", "testdata", "docs"},
	}
}

// LoadConfig reads a JSON config file and overlays it onto Default(), so the
// file names only the fields it changes. Scalar fields (including those inside
// nested objects) merge over the defaults; slice fields are fully replaced
// when present and kept from the defaults when omitted. An explicit empty
// array ("subtypes": []) therefore removes a default, distinct from omitting
// the key. The result is validated before return.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is an operator-supplied config location
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	base := Default()
	cfg := base
	// Clear every slice before decoding. Go's json decoder reuses a non-empty
	// destination slice's existing elements, which would merge a provided
	// array into the default's structs field-by-field (leaking default fields
	// into shorter or partially-specified arrays). Starting from nil forces a
	// clean replacement; omitted arrays stay nil and are restored below.
	cfg.KeywordClasses = nil
	cfg.IDGrammar.Subtypes = nil
	cfg.Tag.CommentMarkers = nil
	cfg.Tag.Collectors = nil
	cfg.Coverage.Rules = nil
	cfg.Classification.Values = nil
	cfg.Waivers.Reasons = nil
	cfg.SkipDirs = nil

	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	// Restore any slice the JSON omitted (still nil) from the defaults; an
	// explicit empty array decoded to a non-nil empty slice and is preserved.
	if cfg.KeywordClasses == nil {
		cfg.KeywordClasses = base.KeywordClasses
	}
	if cfg.IDGrammar.Subtypes == nil {
		cfg.IDGrammar.Subtypes = base.IDGrammar.Subtypes
	}
	if cfg.Tag.CommentMarkers == nil {
		cfg.Tag.CommentMarkers = base.Tag.CommentMarkers
	}
	if cfg.Tag.Collectors == nil {
		cfg.Tag.Collectors = base.Tag.Collectors
	}
	if cfg.Coverage.Rules == nil {
		cfg.Coverage.Rules = base.Coverage.Rules
	}
	if cfg.Classification.Values == nil {
		cfg.Classification.Values = base.Classification.Values
	}
	if cfg.Waivers.Reasons == nil {
		cfg.Waivers.Reasons = base.Waivers.Reasons
	}
	if cfg.SkipDirs == nil {
		cfg.SkipDirs = base.SkipDirs
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

// Validate compiles the dialect's regexes and rejects empty required fields,
// unknown collector languages, and contradictory collector settings. It must
// be called before the config is used; Default() and a clean LoadConfig both
// guarantee a validated config.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.IDGrammar.Pattern) == "" {
		return fmt.Errorf("idGrammar.pattern is required")
	}
	full, err := regexp.Compile(`^` + c.IDGrammar.Pattern + `$`)
	if err != nil {
		return fmt.Errorf("idGrammar.pattern: %w", err)
	}
	catalogHeading, err := regexp.Compile(`^### (` + c.IDGrammar.Pattern + `)(?:\s+—\s+(.*))?$`)
	if err != nil {
		return fmt.Errorf("idGrammar.pattern (catalog heading): %w", err)
	}
	plainHeading, err := regexp.Compile(`^### (` + c.IDGrammar.Pattern + `)\s*$`)
	if err != nil {
		return fmt.Errorf("idGrammar.pattern (plain heading): %w", err)
	}
	if strings.TrimSpace(c.IDGrammar.HeadingPrefix) == "" {
		return fmt.Errorf("idGrammar.headingPrefix is required")
	}
	loose := regexp.MustCompile(`^###\s+` + regexp.QuoteMeta(c.IDGrammar.HeadingPrefix))

	var series *regexp.Regexp
	if c.IDGrammar.SeriesPattern != "" {
		series, err = regexp.Compile(c.IDGrammar.SeriesPattern)
		if err != nil {
			return fmt.Errorf("idGrammar.seriesPattern: %w", err)
		}
		if series.NumSubexp() < 1 {
			return fmt.Errorf("idGrammar.seriesPattern must have one capture group")
		}
	}

	if strings.TrimSpace(c.Catalog.KeywordField) == "" {
		return fmt.Errorf("catalog.keywordField is required")
	}
	if strings.TrimSpace(c.Catalog.SectionField) == "" {
		return fmt.Errorf("catalog.sectionField is required")
	}
	keywordLine := regexp.MustCompile(`^- ` + regexp.QuoteMeta(c.Catalog.KeywordField) + `:\s*([^|]+?)\s*(?:\|.*)?$`)
	sectionLine := regexp.MustCompile(`^- ` + regexp.QuoteMeta(c.Catalog.SectionField) + `:\s*(.*)$`)
	var sectionRef *regexp.Regexp
	if c.Catalog.SectionRefPattern != "" {
		sectionRef, err = regexp.Compile(c.Catalog.SectionRefPattern)
		if err != nil {
			return fmt.Errorf("catalog.sectionRefPattern: %w", err)
		}
	}

	if strings.TrimSpace(c.Tag.Keyword) == "" {
		return fmt.Errorf("tag.keyword is required")
	}
	if len(c.Tag.Collectors) == 0 {
		return fmt.Errorf("tag.collectors must list at least one collector")
	}
	for i, col := range c.Tag.Collectors {
		switch col.Lang {
		case "go":
			if len(col.FuncPrefixes) == 0 {
				return fmt.Errorf("tag.collectors[%d] (go): funcPrefixes is required", i)
			}
		case "comment":
			if col.FileSuffix == "" {
				return fmt.Errorf("tag.collectors[%d] (comment): fileSuffix is required", i)
			}
			if len(col.TestMarkers) == 0 {
				return fmt.Errorf("tag.collectors[%d] (comment): testMarkers is required", i)
			}
			if col.NamePattern != "" {
				np, perr := regexp.Compile(col.NamePattern)
				if perr != nil {
					return fmt.Errorf("tag.collectors[%d].namePattern: %w", i, perr)
				}
				if np.NumSubexp() < 1 {
					return fmt.Errorf("tag.collectors[%d].namePattern must have one capture group", i)
				}
			}
		default:
			return fmt.Errorf("tag.collectors[%d]: unknown lang %q (want go or comment)", i, col.Lang)
		}
	}

	if strings.TrimSpace(c.Coverage.Default) == "" {
		return fmt.Errorf("coverage.default is required")
	}

	if strings.TrimSpace(c.Waivers.ReasonField) == "" {
		return fmt.Errorf("waivers.reasonField is required")
	}
	if strings.TrimSpace(c.Waivers.RationaleField) == "" {
		return fmt.Errorf("waivers.rationaleField is required")
	}
	reasonField := regexp.MustCompile(`^- ` + regexp.QuoteMeta(c.Waivers.ReasonField) + `:\s*(.*)$`)

	if strings.TrimSpace(c.Classification.ClassField) == "" {
		return fmt.Errorf("classification.classField is required")
	}
	if strings.TrimSpace(c.Classification.ReasonField) == "" {
		return fmt.Errorf("classification.reasonField is required")
	}

	c.compiled = compiledConfig{
		fullID:            full,
		catalogHeading:    catalogHeading,
		plainHeading:      plainHeading,
		looseHeading:      loose,
		keywordLine:       keywordLine,
		sectionLine:       sectionLine,
		sectionRef:        sectionRef,
		series:            series,
		reasonField:       reasonField,
		rationalePrefix:   "- " + c.Waivers.RationaleField + ":",
		classClassField:   "- " + c.Classification.ClassField + ":",
		classReasonPrefix: "- " + c.Classification.ReasonField + ":",
	}
	return nil
}

// seriesOf returns the series segment of an ID per SeriesPattern, or "" if the
// ID does not match (or scoping is disabled).
func (c *Config) seriesOf(id string) string {
	if c.compiled.series == nil {
		return ""
	}
	m := c.compiled.series.FindStringSubmatch(id)
	if m == nil {
		return ""
	}
	return m[1]
}

// classFromKeyword maps a catalog Keyword line to its strongest policy class.
func (c *Config) classFromKeyword(kw string) string {
	for _, kc := range c.KeywordClasses {
		for _, k := range kc.Keywords {
			if strings.Contains(kw, k) {
				return kc.Class
			}
		}
	}
	return ""
}

// classFromID classifies subtype IDs (those carrying no Keyword line).
func (c *Config) classFromID(id string) string {
	for _, st := range c.IDGrammar.Subtypes {
		if strings.Contains(id, st.Marker) {
			return st.Class
		}
	}
	return ""
}

// orderedClasses returns the policy classes in matrix-display order: keyword
// classes first (in config order), then subtype classes.
func (c *Config) orderedClasses() []string {
	var classes []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			classes = append(classes, s)
		}
	}
	for _, kc := range c.KeywordClasses {
		add(kc.Class)
	}
	for _, st := range c.IDGrammar.Subtypes {
		add(st.Class)
	}
	return classes
}
