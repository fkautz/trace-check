package tracecheck

// Requirement is one catalog entry.
type Requirement struct {
	ID      string
	Title   string
	Keyword string // raw Keyword line content; empty for subtype entries
	Section string
	Class   string // "must", "should", "may", or a subtype class; "" if unclassifiable
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
}

// ClassEntry is one requirement's classification.
type ClassEntry struct {
	ID        string
	Class     string
	HasReason bool
}
