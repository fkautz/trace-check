package tracecheck

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
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
	IDGrammar      IDGrammar          `json:"idGrammar"`
	Catalog        CatalogConfig      `json:"catalog"`
	KeywordClasses []KeywordClass     `json:"keywordClasses"`
	Tag            TagConfig          `json:"tag"`
	Coverage       CoverageConfig     `json:"coverage"`
	Waivers        WaiverConfig       `json:"waivers"`
	Classification ClassConfig        `json:"classification"`
	Matrix         MatrixConfig       `json:"matrix"`
	Architecture   ArchitectureConfig `json:"architecture"`
	Policy         PolicyConfig       `json:"policy"`
	Strict         StrictConfig       `json:"strict"`
	Profiles       map[string]Profile `json:"profiles"`
	SkipDirs       []string           `json:"skipDirs"`

	compiled compiledConfig
	// sourcePath/sourceSHA256 bind provenance to the exact bytes LoadConfig
	// decoded. They are deliberately excluded from the effective config JSON.
	sourcePath   string
	sourceSHA256 string
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
	// HeadingCandidatePattern is an optional regex matched at the start of the
	// text after "### ". When set, it replaces HeadingPrefix for malformed-
	// heading detection. This lets multi-series catalogs recognize every
	// ID-shaped heading without sharing one literal prefix.
	HeadingCandidatePattern string `json:"headingCandidatePattern"`
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
	// Fields are optional metadata fields parsed as "- Name: value" under each
	// requirement. Used for architecture adherence (Component, Phase, Kind, …).
	// Names must not collide with keywordField or sectionField.
	Fields []CatalogField `json:"fields"`
}

// CatalogField describes one optional catalog metadata field.
type CatalogField struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	// Enum is a closed set of allowed values. Empty means any non-empty value
	// is accepted when the field is present (or required).
	Enum []string `json:"enum"`
	// EnumFrom loads allowed values from the architecture registry:
	// "architecture.components" or "architecture.invariants". Requires
	// architecture.path (or Scope architecture resolution) to be set.
	EnumFrom string `json:"enumFrom"`
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
	ReasonField    string `json:"reasonField"`
	RationaleField string `json:"rationaleField"`
	// CoversField is the structured covered-by target line label (default
	// "Covers"). Empty disables structured covers parsing.
	CoversField string `json:"coversField"`
	// CoveredByReason is the reason value that means "covered by another
	// requirement" (default "covered-by").
	CoveredByReason string `json:"coveredByReason"`
	// RequireCoversForCoveredBy requires a Covers line when Reason is
	// CoveredByReason. Off by default for backward compatibility.
	RequireCoversForCoveredBy bool `json:"requireCoversForCoveredBy"`
	// CoversForbidTargetReasons flags a Covers target whose OWN waiver reason is
	// in this list — a covered-by must point at an active covering requirement,
	// not one that was itself excused away. Set e.g. ["superseded"] to catch a
	// covered-by that still names a retired/superseded requirement. Empty (the
	// default) disables the check. Each entry must be an allowed waiver reason.
	CoversForbidTargetReasons []string `json:"coversForbidTargetReasons"`
	Reasons                   []string `json:"reasons"`
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

// ArchitectureConfig points at a closed vocabulary of components and
// invariants. Empty Path disables architecture registry loading.
type ArchitectureConfig struct {
	// Path is relative to Scope.Root (or absolute). Empty = dormant.
	Path string `json:"path"`
	// ComponentSection is the ##/### heading title for components (default "Components").
	ComponentSection string `json:"componentSection"`
	// InvariantSection is the ##/### heading title for invariants (default "Invariants").
	InvariantSection string `json:"invariantSection"`
}

// PolicyConfig holds when→coverage rules driven by catalog metadata.
type PolicyConfig struct {
	Rules []PolicyRule `json:"rules"`
	// WaiverReasonsSatisfy lists the waiver reasons that satisfy a matching
	// rule's StrictRequiresCoverageClass (and a classification value's
	// StrictRequiresCoverageClass). Default: covered-by, documented-deviation —
	// a deliberate excusal counts at freeze time, but a not-implemented
	// placeholder does not. The covered-by reason is evidence by proxy: it
	// only satisfies when the waiver's Covers target itself carries a tag of
	// the required coverage class. An explicit empty array means no waiver
	// satisfies a policy coverage requirement. Every entry must be an allowed
	// waiver reason (waivers.reasons).
	WaiverReasonsSatisfy []string `json:"waiverReasonsSatisfy"`
}

// PolicyRule applies coverage constraints when a requirement's metadata matches.
// When is AND across fields, OR within a field's value list. Special field
// "KeywordClass" matches Requirement.Class; other keys match Meta fields.
type PolicyRule struct {
	When                        map[string][]string `json:"when"`
	StrictRequiresCoverageClass string              `json:"strictRequiresCoverageClass"`
	ForbidsCoverageClass        string              `json:"forbidsCoverageClass"`
	// AllowUncovered: under -strict, matching requirements need no tag/waiver.
	AllowUncovered bool `json:"allowUncovered"`
}

// StrictConfig scopes full-coverage enforcement under -strict.
// Empty Phases and KeywordClasses mean "all requirements" (legacy behaviour).
type StrictConfig struct {
	// Phases: only requirements whose Phase meta field is in this list need
	// coverage under -strict. Empty = no phase filter.
	Phases []string `json:"phases"`
	// KeywordClasses: only these policy classes need coverage under -strict
	// (e.g. ["must"]). Empty = no keyword-class filter.
	KeywordClasses []string `json:"keywordClasses"`
	// PhaseField is the catalog metadata field holding the phase (default "Phase").
	PhaseField string `json:"phaseField"`
	// WaiverReasonsSatisfy controls which waiver reasons satisfy base strict
	// coverage. Nil (omitted) preserves the legacy behavior where any valid
	// waiver satisfies. An explicit empty list means no waiver satisfies. A
	// covered-by reason also requires its Covers target to carry a tagged test.
	WaiverReasonsSatisfy []string `json:"waiverReasonsSatisfy"`
}

// Profile is a named strictness/filter pack selected with -profile.
type Profile struct {
	// Strict forces -strict when the profile is selected.
	Strict bool `json:"strict"`
	// StrictPhases overrides config strict.phases for this profile when non-empty.
	StrictPhases []string `json:"strictPhases"`
	// StrictKeywordClasses overrides config strict.keywordClasses when non-empty.
	StrictKeywordClasses []string `json:"strictKeywordClasses"`
}

// MatrixConfig labels the coverage columns of the generated matrix.
//
// Two-column mode (default): a tag whose class equals SecondaryClass lands in
// the secondary column; every other class lands in the primary column.
//
// Multi-column mode: when CoverageColumns is non-empty, one column per entry
// is emitted and the two-column gap lists are replaced by per-class counts.
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
	// CoverageColumns, when non-empty, replaces primary/secondary columns.
	CoverageColumns []MatrixColumn `json:"coverageColumns"`
	// GroupBy is a catalog meta field name; when set, the matrix is sectioned
	// by that field's value (e.g. "Component", "Phase").
	GroupBy string `json:"groupBy"`
}

// MatrixColumn is one coverage-class column in multi-column matrix mode.
type MatrixColumn struct {
	Class string `json:"class"`
	Label string `json:"label"`
}

// compiledConfig holds the regexes built from Config during Validate.
type compiledConfig struct {
	fullID            *regexp.Regexp
	catalogHeading    *regexp.Regexp
	plainHeading      *regexp.Regexp // headings with no title (waivers/classification)
	headingCandidate  *regexp.Regexp
	keywordLine       *regexp.Regexp
	sectionLine       *regexp.Regexp
	sectionRef        *regexp.Regexp // nil if SectionRefPattern is empty
	series            *regexp.Regexp // nil if SeriesPattern is empty
	reasonField       *regexp.Regexp // waiver reason line
	coversField       *regexp.Regexp // nil if CoversField empty
	rationalePrefix   string
	classClassField   string
	classReasonPrefix string
	// metaFieldLines maps field name -> compiled "^- Name:\s*(.*)$"
	metaFieldLines map[string]*regexp.Regexp
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
			ReasonField:               "Reason",
			RationaleField:            "Rationale",
			CoversField:               "Covers",
			CoveredByReason:           "covered-by",
			RequireCoversForCoveredBy: false,
			Reasons:                   []string{"deployment-guidance", "not-implemented", "covered-by", "documented-deviation", "foundational"},
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
		Architecture: ArchitectureConfig{
			ComponentSection: "Components",
			InvariantSection: "Invariants",
		},
		Policy: PolicyConfig{
			WaiverReasonsSatisfy: []string{"covered-by", "documented-deviation"},
		},
		Strict: StrictConfig{
			PhaseField: "Phase",
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
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolve config source %s: %w", path, err)
	}
	absolutePath = filepath.Clean(absolutePath)
	data, err := os.ReadFile(absolutePath) // #nosec G304 -- path is an operator-supplied config location
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
	cfg.Waivers.CoversForbidTargetReasons = nil
	cfg.Catalog.Fields = nil
	cfg.Matrix.CoverageColumns = nil
	cfg.Policy.Rules = nil
	cfg.Policy.WaiverReasonsSatisfy = nil
	cfg.Strict.Phases = nil
	cfg.Strict.KeywordClasses = nil
	cfg.Profiles = nil
	cfg.SkipDirs = nil

	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("parse config %s: multiple JSON values", path)
		}
		return Config{}, fmt.Errorf("parse config %s: trailing content: %w", path, err)
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
	if cfg.Catalog.Fields == nil {
		cfg.Catalog.Fields = base.Catalog.Fields
	}
	if cfg.Matrix.CoverageColumns == nil {
		cfg.Matrix.CoverageColumns = base.Matrix.CoverageColumns
	}
	if cfg.Policy.Rules == nil {
		cfg.Policy.Rules = base.Policy.Rules
	}
	if cfg.Policy.WaiverReasonsSatisfy == nil {
		// Omitted: default to the built-in list filtered to the (possibly
		// overridden) allowed waiver reasons, so a custom reasons vocabulary
		// does not have to name this field to stay valid. An explicit empty
		// array remains "no waiver satisfies".
		filtered := []string{}
		for _, r := range base.Policy.WaiverReasonsSatisfy {
			if stringIn(cfg.Waivers.Reasons, r) {
				filtered = append(filtered, r)
			}
		}
		cfg.Policy.WaiverReasonsSatisfy = filtered
	}
	if cfg.Strict.Phases == nil {
		cfg.Strict.Phases = base.Strict.Phases
	}
	if cfg.Strict.KeywordClasses == nil {
		cfg.Strict.KeywordClasses = base.Strict.KeywordClasses
	}
	if cfg.Profiles == nil {
		cfg.Profiles = base.Profiles
	}
	if cfg.SkipDirs == nil {
		cfg.SkipDirs = base.SkipDirs
	}
	// Scalar defaults that JSON may zero out only if the parent object was
	// fully replaced are preserved via merge of nested structs already.

	// Fill empty waiver covers defaults when the user overrode Waivers but
	// omitted the new fields (zero values).
	if cfg.Waivers.CoveredByReason == "" {
		cfg.Waivers.CoveredByReason = base.Waivers.CoveredByReason
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	cfg.sourcePath = absolutePath
	cfg.sourceSHA256 = "sha256:" + hex.EncodeToString(sum[:])
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
	// HeadingCandidatePattern supports multi-series catalogs that have no common
	// literal HeadingPrefix. When neither is set, malformed-heading detection is
	// disabled.
	var headingCandidate *regexp.Regexp
	if c.IDGrammar.HeadingCandidatePattern != "" {
		headingCandidate, err = regexp.Compile(`^(?:` + c.IDGrammar.HeadingCandidatePattern + `)`)
		if err != nil {
			return fmt.Errorf("idGrammar.headingCandidatePattern: %w", err)
		}
	} else if strings.TrimSpace(c.IDGrammar.HeadingPrefix) != "" {
		headingCandidate = regexp.MustCompile(`^` + regexp.QuoteMeta(c.IDGrammar.HeadingPrefix))
	} else {
		// Never matches — malformed-prefix reporting is off.
		headingCandidate = regexp.MustCompile(`^\b\B`) // impossible
	}

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

	for i, r := range c.Policy.WaiverReasonsSatisfy {
		if !stringIn(c.Waivers.Reasons, r) {
			return fmt.Errorf("policy.waiverReasonsSatisfy[%d]: %q is not an allowed waiver reason (waivers.reasons)", i, r)
		}
	}
	for i, r := range c.Strict.WaiverReasonsSatisfy {
		if !stringIn(c.Waivers.Reasons, r) {
			return fmt.Errorf("strict.waiverReasonsSatisfy[%d]: %q is not an allowed waiver reason (waivers.reasons)", i, r)
		}
	}
	for i, r := range c.Waivers.CoversForbidTargetReasons {
		if !stringIn(c.Waivers.Reasons, r) {
			return fmt.Errorf("waivers.coversForbidTargetReasons[%d]: %q is not an allowed waiver reason (waivers.reasons)", i, r)
		}
	}

	metaFieldLines := map[string]*regexp.Regexp{}
	metaFields := map[string]CatalogField{}
	seenField := map[string]bool{}
	for i, f := range c.Catalog.Fields {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			return fmt.Errorf("catalog.fields[%d].name is required", i)
		}
		if name != f.Name {
			return fmt.Errorf("catalog.fields[%d].name %q has leading or trailing whitespace", i, f.Name)
		}
		if name == c.Catalog.KeywordField || name == c.Catalog.SectionField {
			return fmt.Errorf("catalog.fields[%d].name %q collides with keywordField/sectionField", i, name)
		}
		if seenField[name] {
			return fmt.Errorf("catalog.fields: duplicate field %q", name)
		}
		seenField[name] = true
		metaFields[name] = f
		switch f.EnumFrom {
		case "", "architecture.components", "architecture.invariants":
		default:
			return fmt.Errorf("catalog.fields[%d].enumFrom: unknown %q (want architecture.components or architecture.invariants)", i, f.EnumFrom)
		}
		if f.EnumFrom != "" && len(f.Enum) > 0 {
			return fmt.Errorf("catalog.fields[%d]: enum and enumFrom are mutually exclusive", i)
		}
		metaFieldLines[name] = regexp.MustCompile(`^- ` + regexp.QuoteMeta(name) + `:\s*(.*)$`)
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
	for i, rule := range c.Coverage.Rules {
		if strings.TrimSpace(rule.Class) == "" {
			return fmt.Errorf("coverage.rules[%d].class is required", i)
		}
	}
	coverageClasses := c.coverageClasses()

	if strings.TrimSpace(c.Waivers.ReasonField) == "" {
		return fmt.Errorf("waivers.reasonField is required")
	}
	if strings.TrimSpace(c.Waivers.RationaleField) == "" {
		return fmt.Errorf("waivers.rationaleField is required")
	}
	reasonField := regexp.MustCompile(`^- ` + regexp.QuoteMeta(c.Waivers.ReasonField) + `:\s*(.*)$`)
	var coversField *regexp.Regexp
	if c.Waivers.CoversField != "" {
		coversField = regexp.MustCompile(`^- ` + regexp.QuoteMeta(c.Waivers.CoversField) + `:\s*(.*)$`)
	}
	if c.Waivers.CoveredByReason == "" {
		c.Waivers.CoveredByReason = "covered-by"
	}
	if c.Strict.PhaseField == "" {
		c.Strict.PhaseField = "Phase"
	}

	if strings.TrimSpace(c.Classification.ClassField) == "" {
		return fmt.Errorf("classification.classField is required")
	}
	if strings.TrimSpace(c.Classification.ReasonField) == "" {
		return fmt.Errorf("classification.reasonField is required")
	}

	for i, col := range c.Matrix.CoverageColumns {
		if strings.TrimSpace(col.Class) == "" {
			return fmt.Errorf("matrix.coverageColumns[%d].class is required", i)
		}
		if strings.TrimSpace(col.Label) == "" {
			return fmt.Errorf("matrix.coverageColumns[%d].label is required", i)
		}
	}
	if c.Matrix.GroupBy != "" && !seenField[c.Matrix.GroupBy] {
		return fmt.Errorf("matrix.groupBy: unknown catalog field %q", c.Matrix.GroupBy)
	}

	keywordClassSet := map[string]bool{}
	for _, class := range c.orderedClasses() {
		keywordClassSet[class] = true
	}
	if err := c.validatePhaseFilter("strict.phases", c.Strict.Phases); err != nil {
		return err
	}
	if err := c.validateKeywordClassFilter("strict.keywordClasses", c.Strict.KeywordClasses); err != nil {
		return err
	}
	profileNames := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		profileNames = append(profileNames, name)
	}
	sort.Strings(profileNames)
	for _, name := range profileNames {
		profile := c.Profiles[name]
		if err := c.validatePhaseFilter(fmt.Sprintf("profiles[%q].strictPhases", name), profile.StrictPhases); err != nil {
			return err
		}
		if err := c.validateKeywordClassFilter(fmt.Sprintf("profiles[%q].strictKeywordClasses", name), profile.StrictKeywordClasses); err != nil {
			return err
		}
	}
	for i, rule := range c.Policy.Rules {
		for field, values := range rule.When {
			if len(values) == 0 {
				return fmt.Errorf("policy.rules[%d].when[%q] must list at least one value", i, field)
			}
			if field == "KeywordClass" {
				for _, value := range values {
					if !keywordClassSet[value] {
						return fmt.Errorf("policy.rules[%d].when: unknown KeywordClass value %q", i, value)
					}
				}
				continue
			}
			meta, ok := metaFields[field]
			if !ok {
				return fmt.Errorf("policy.rules[%d].when: unknown field %q", i, field)
			}
			if len(meta.Enum) > 0 {
				for _, value := range values {
					if !stringIn(meta.Enum, value) {
						return fmt.Errorf("policy.rules[%d].when: unknown %s value %q", i, field, value)
					}
				}
			}
		}
		for _, class := range []string{rule.StrictRequiresCoverageClass, rule.ForbidsCoverageClass} {
			if class != "" && !coverageClasses[class] {
				return fmt.Errorf("policy.rules[%d]: unknown coverage class %q", i, class)
			}
		}
		if rule.AllowUncovered && rule.StrictRequiresCoverageClass != "" {
			return fmt.Errorf("policy.rules[%d]: allowUncovered cannot be combined with strictRequiresCoverageClass", i)
		}
		if rule.StrictRequiresCoverageClass != "" && rule.StrictRequiresCoverageClass == rule.ForbidsCoverageClass {
			return fmt.Errorf("policy.rules[%d]: requires and forbids coverage class %q", i, rule.StrictRequiresCoverageClass)
		}
	}

	usesArchEnum := false
	for _, f := range c.Catalog.Fields {
		if f.EnumFrom != "" {
			usesArchEnum = true
			break
		}
	}
	if usesArchEnum && strings.TrimSpace(c.Architecture.Path) == "" {
		// Architecture path may still be supplied at Check time via Scope;
		// only warn at Validate if we can. Allow empty here; Check enforces.
	}

	c.compiled = compiledConfig{
		fullID:            full,
		catalogHeading:    catalogHeading,
		plainHeading:      plainHeading,
		headingCandidate:  headingCandidate,
		keywordLine:       keywordLine,
		sectionLine:       sectionLine,
		sectionRef:        sectionRef,
		series:            series,
		reasonField:       reasonField,
		coversField:       coversField,
		rationalePrefix:   "- " + c.Waivers.RationaleField + ":",
		classClassField:   "- " + c.Classification.ClassField + ":",
		classReasonPrefix: "- " + c.Classification.ReasonField + ":",
		metaFieldLines:    metaFieldLines,
	}
	return nil
}

func (c *Config) validatePhaseFilter(label string, values []string) error {
	if len(values) == 0 {
		return nil
	}
	var field CatalogField
	found := false
	for _, candidate := range c.Catalog.Fields {
		if candidate.Name == c.Strict.PhaseField {
			field = candidate
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("strict.phaseField: unknown field %q", c.Strict.PhaseField)
	}
	if len(field.Enum) == 0 {
		return nil
	}
	for _, value := range values {
		if !stringIn(field.Enum, value) {
			return fmt.Errorf("%s: unknown %s value %q", label, c.Strict.PhaseField, value)
		}
	}
	return nil
}

func (c *Config) validateKeywordClassFilter(label string, values []string) error {
	known := map[string]bool{}
	for _, class := range c.orderedClasses() {
		known[class] = true
	}
	for _, value := range values {
		if !known[value] {
			return fmt.Errorf("%s: unknown keyword class %q", label, value)
		}
	}
	return nil
}

// ValidateScope checks effective CLI/profile overrides after they have been
// applied. Check calls this defensively so programmatic callers cannot bypass
// the same fail-closed validation enforced by the CLI.
func (c *Config) ValidateScope(scope Scope) error {
	scope = normalizeScopePaths(scope)
	if err := c.validateScopePaths(scope); err != nil {
		return err
	}
	return c.validateScopeFilters(scope)
}

func (c *Config) validateScopeFilters(scope Scope) error {
	if scope.CheckOutput && scope.Out == "" && scope.OutJSON == "" {
		return fmt.Errorf("check-output requires -out and/or -out-json")
	}
	if err := c.validatePhaseFilter("scope.strictPhases", c.effectiveStrictPhases(scope)); err != nil {
		return err
	}
	return c.validateKeywordClassFilter("scope.strictKeywordClasses", c.effectiveStrictKeywordClasses(scope))
}

func (c *Config) validateScopePaths(scope Scope) error {
	configPath, err := c.effectiveConfigPath(scope)
	if err != nil {
		return err
	}
	paths := []struct {
		label string
		path  string
	}{
		{label: "out", path: scope.Out},
		{label: "out-json", path: scope.OutJSON},
		{label: "problems-json", path: scope.ProblemsJSON},
	}
	seenPaths := map[string]string{}
	type existingOutput struct {
		label string
		path  string
		info  os.FileInfo
	}
	var existingOutputs []existingOutput
	for _, item := range paths {
		if item.path == "" {
			continue
		}
		resolved, err := filepath.Abs(resolveScopePath(scope.Root, item.path))
		if err != nil {
			return fmt.Errorf("resolve %s path: %w", item.label, err)
		}
		resolved = filepath.Clean(resolved)
		identity := scopePathIdentity(resolved)
		if previous, ok := seenPaths[identity]; ok {
			return fmt.Errorf("output path collision: %s and %s both resolve to %s", previous, item.label, resolved)
		}
		if info, statErr := os.Stat(resolved); statErr == nil {
			for _, previous := range existingOutputs {
				if os.SameFile(previous.info, info) {
					return fmt.Errorf("output path collision: %s (%s) and %s (%s) refer to the same file", previous.label, previous.path, item.label, resolved)
				}
			}
			existingOutputs = append(existingOutputs, existingOutput{label: item.label, path: resolved, info: info})
		}
		seenPaths[identity] = item.label
	}
	inputs := []struct {
		label string
		path  string
	}{
		{label: "catalog", path: scope.Catalog},
		{label: "classification", path: scope.Classification},
		{label: "waivers", path: scope.Waivers},
		{label: "architecture", path: c.effectiveArchitecturePath(scope)},
		{label: "config", path: configPath},
	}
	for _, input := range inputs {
		if input.path == "" {
			continue
		}
		// Scope input paths are consumed exactly as supplied by the parsers.
		// The CLI has already joined root-relative inputs to Root; joining here
		// again would turn a relative Root such as "repo" into
		// "repo/repo/spec/requirements.md" and miss destructive collisions.
		resolved, err := filepath.Abs(input.path)
		if err != nil {
			return fmt.Errorf("resolve %s path: %w", input.label, err)
		}
		resolved = filepath.Clean(resolved)
		if output, ok := seenPaths[scopePathIdentity(resolved)]; ok {
			return fmt.Errorf("output/input path collision: %s and %s both resolve to %s", output, input.label, resolved)
		}
		if info, statErr := os.Stat(resolved); statErr == nil {
			for _, output := range existingOutputs {
				if os.SameFile(output.info, info) {
					return fmt.Errorf("output/input path collision: %s (%s) and %s (%s) refer to the same file", output.label, output.path, input.label, resolved)
				}
			}
		}
	}
	return nil
}

// scopePathIdentity resolves existing symlinks, symlinked parent directories,
// and a dangling final symlink. Outputs may not exist yet, so Abs/Clean and
// os.SameFile alone cannot prevent two declared paths from writing one file.
func scopePathIdentity(path string) string {
	identity := resolveScopePathIdentity(path, 0)
	if scopeFilesystemCaseInsensitive(identity) {
		return strings.ToLower(identity)
	}
	return identity
}

func resolveScopePathIdentity(path string, depth int) string {
	cleaned := filepath.Clean(path)
	if depth > 32 {
		return cleaned
	}
	if evaluated, err := filepath.EvalSymlinks(cleaned); err == nil {
		absolute, absErr := filepath.Abs(evaluated)
		if absErr == nil {
			return filepath.Clean(absolute)
		}
		return filepath.Clean(evaluated)
	}
	if target, err := os.Readlink(cleaned); err == nil {
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(cleaned), target)
		}
		return resolveScopePathIdentity(target, depth+1)
	}
	parent := filepath.Dir(cleaned)
	if parent == cleaned {
		return cleaned
	}
	return filepath.Join(resolveScopePathIdentity(parent, depth+1), filepath.Base(cleaned))
}

func scopeFilesystemCaseInsensitive(path string) bool {
	dir := filepath.Dir(path)
	for {
		base := filepath.Base(dir)
		alternate := swapFirstASCIICase(base)
		if alternate != base {
			info, err := os.Stat(dir)
			alternateInfo, alternateErr := os.Stat(filepath.Join(filepath.Dir(dir), alternate))
			switch {
			case err == nil && alternateErr == nil:
				return os.SameFile(info, alternateInfo)
			case err == nil && os.IsNotExist(alternateErr):
				return false
			case os.IsNotExist(err) && alternateErr == nil:
				return false
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return runtime.GOOS == "darwin" || runtime.GOOS == "windows"
}

func swapFirstASCIICase(value string) string {
	for i := 0; i < len(value); i++ {
		ch := value[i]
		switch {
		case ch >= 'a' && ch <= 'z':
			return value[:i] + string(ch-'a'+'A') + value[i+1:]
		case ch >= 'A' && ch <= 'Z':
			return value[:i] + string(ch-'A'+'a') + value[i+1:]
		}
	}
	return value
}

func (c *Config) effectiveConfigPath(scope Scope) (string, error) {
	if c.sourcePath == "" {
		return scope.ConfigPath, nil
	}
	if scope.ConfigPath == "" {
		return c.sourcePath, nil
	}
	resolved, err := filepath.Abs(scope.ConfigPath)
	if err != nil {
		return "", fmt.Errorf("resolve config path: %w", err)
	}
	if filepath.Clean(resolved) != c.sourcePath {
		return "", fmt.Errorf("config path %s does not match loaded config source %s", scope.ConfigPath, c.sourcePath)
	}
	return c.sourcePath, nil
}

func (c *Config) coverageClasses() map[string]bool {
	classes := map[string]bool{}
	if c.Coverage.Default != "" {
		classes[c.Coverage.Default] = true
	}
	for _, rule := range c.Coverage.Rules {
		if rule.Class != "" {
			classes[rule.Class] = true
		}
	}
	return classes
}

func (c *Config) validateActiveClassification() []string {
	coverageClasses := c.coverageClasses()
	seenNames := map[string]bool{}
	var problems []string
	for i, value := range c.Classification.Values {
		if seenNames[value.Name] {
			problems = append(problems, fmt.Sprintf("classification.values[%d]: duplicate value %q", i, value.Name))
		}
		seenNames[value.Name] = true
		for _, class := range []string{value.StrictRequiresCoverageClass, value.ForbidsCoverageClass} {
			if class != "" && !coverageClasses[class] {
				problems = append(problems, fmt.Sprintf("classification.values[%d]: unknown coverage class %q", i, class))
			}
		}
		if value.StrictRequiresCoverageClass != "" && value.StrictRequiresCoverageClass == value.ForbidsCoverageClass {
			problems = append(problems, fmt.Sprintf("classification.values[%d]: requires and forbids coverage class %q", i, value.StrictRequiresCoverageClass))
		}
	}
	return problems
}

var markdownH3Prefix = regexp.MustCompile(`^###\s+`)

// isHeadingCandidate reports whether a markdown H3 looks like an attempted
// requirement ID heading. The configured candidate regex is applied to the
// heading text itself, so a natural leading ^ anchor remains meaningful.
func (c *Config) isHeadingCandidate(line string) bool {
	loc := markdownH3Prefix.FindStringIndex(line)
	return loc != nil && c.compiled.headingCandidate.MatchString(line[loc[1]:])
}

// validatePolicyArchitectureValues checks policy selector values whose catalog
// fields draw their vocabulary from the loaded architecture registry. Static
// enum selectors are rejected earlier by Validate.
func (c *Config) validatePolicyArchitectureValues(arch *Architecture) []string {
	fields := map[string]CatalogField{}
	for _, field := range c.Catalog.Fields {
		fields[field.Name] = field
	}

	var problems []string
	for i, rule := range c.Policy.Rules {
		for name, values := range rule.When {
			field, ok := fields[name]
			if !ok || field.EnumFrom == "" {
				continue
			}
			allowed, err := c.fieldAllowedValues(field, arch)
			if err != nil {
				problems = append(problems, fmt.Sprintf("policy.rules[%d].when[%s]: %v", i, name, err))
				continue
			}
			for _, value := range values {
				if !allowed[value] {
					problems = append(problems, fmt.Sprintf("policy.rules[%d].when: unknown %s value %q", i, name, value))
				}
			}
		}
	}
	return problems
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

// phaseFieldName returns the catalog meta field used for phase filtering.
func (c *Config) phaseFieldName() string {
	if c.Strict.PhaseField != "" {
		return c.Strict.PhaseField
	}
	return "Phase"
}

// ApplyProfile merges a named profile into scope filters. Unknown name errors.
func (c *Config) ApplyProfile(name string, scope *Scope) error {
	if name == "" {
		return nil
	}
	if c.Profiles == nil {
		return fmt.Errorf("unknown profile %q (no profiles configured)", name)
	}
	p, ok := c.Profiles[name]
	if !ok {
		return fmt.Errorf("unknown profile %q", name)
	}
	scope.Profile = name
	if p.Strict {
		scope.Strict = true
	}
	if len(p.StrictPhases) > 0 {
		scope.StrictPhases = append([]string(nil), p.StrictPhases...)
	}
	if len(p.StrictKeywordClasses) > 0 {
		scope.StrictKeywordClasses = append([]string(nil), p.StrictKeywordClasses...)
	}
	return nil
}

// effectiveStrictPhases returns CLI/profile phases, else config strict.phases.
func (c *Config) effectiveStrictPhases(scope Scope) []string {
	if len(scope.StrictPhases) > 0 {
		return scope.StrictPhases
	}
	return c.Strict.Phases
}

// effectiveStrictKeywordClasses returns CLI/profile classes, else config.
func (c *Config) effectiveStrictKeywordClasses(scope Scope) []string {
	if len(scope.StrictKeywordClasses) > 0 {
		return scope.StrictKeywordClasses
	}
	return c.Strict.KeywordClasses
}

// needsStrictCoverage reports whether r must be tagged or waived under -strict.
func (c *Config) needsStrictCoverage(r Requirement, scope Scope) bool {
	if !scope.Strict {
		return false
	}
	phases := c.effectiveStrictPhases(scope)
	if len(phases) > 0 {
		if !stringIn(phases, r.MetaValue(c.phaseFieldName())) {
			return false
		}
	}
	classes := c.effectiveStrictKeywordClasses(scope)
	if len(classes) > 0 {
		if !stringIn(classes, r.Class) {
			return false
		}
	}
	for _, rule := range c.Policy.Rules {
		if rule.AllowUncovered && policyMatches(rule, r) {
			return false
		}
	}
	return true
}
