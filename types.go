package tracecheck

// Requirement is one catalog entry.
type Requirement struct {
	ID      string
	Title   string
	Keyword string // raw Keyword line content; empty for subtype entries
	Section string
	Class   string // "must", "should", "may", or a subtype class; "" if unclassifiable
	// Meta holds optional catalog fields configured in CatalogConfig.Fields
	// (e.g. Component, Phase, Kind, Invariant). Keyword and Section remain
	// first-class and are not duplicated here.
	Meta map[string]string
}

// MetaValue returns the named catalog metadata field, or "".
func (r Requirement) MetaValue(name string) string {
	if r.Meta == nil {
		return ""
	}
	return r.Meta[name]
}

// TagRef is one test function that tags a requirement.
type TagRef struct {
	File string // slash-normalized path relative to the scan root
	Func string
	// Class is the coverage class assigned by Config.Coverage (e.g. "unit" or
	// "blackbox").
	Class string
}

// WaiverEntry is one waiver.
type WaiverEntry struct {
	ID           string
	Reason       string
	HasRationale bool
	// Covers holds the structured covered-by target IDs (from the Covers
	// field, comma-separated). Empty when the waiver does not use structured
	// covers. A covered-by composite may name several covering requirements.
	Covers []string
}

// ClassEntry is one requirement's classification.
type ClassEntry struct {
	ID        string
	Class     string
	HasReason bool
}

// Architecture is a closed vocabulary of component and invariant names loaded
// from an architecture registry file. Empty when architecture checking is off.
type Architecture struct {
	Components []string
	Invariants []string
	// componentSet / invariantSet are filled by LoadArchitecture for O(1) lookup.
	componentSet map[string]bool
	invariantSet map[string]bool
}

// HasComponent reports whether name is a registered architecture component.
func (a *Architecture) HasComponent(name string) bool {
	if a == nil {
		return false
	}
	return a.componentSet[name]
}

// HasInvariant reports whether name is a registered architecture invariant.
func (a *Architecture) HasInvariant(name string) bool {
	if a == nil {
		return false
	}
	return a.invariantSet[name]
}
