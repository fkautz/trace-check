package tracecheck

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// Scope selects one traceability scope: a catalog with its classification and
// waivers, the tree to scan for tags, and a matrix output path. The same
// dialect (Config) can validate several scopes (e.g. a core and a server
// catalog); tag checking is filtered to the ID series present in the loaded
// catalog, so one scope ignores another scope's tags.
type Scope struct {
	Root           string // repository root scanned for tags
	Catalog        string // path to the catalog markdown
	Classification string // path to the classification markdown ("" or absent → dormant)
	Waivers        string // path to the waivers markdown ("" or absent → none)
	Out            string // matrix output path relative to Root; "" disables
	Strict         bool   // enforce full coverage + per-class scenario policy
}

// Check reconciles the catalog, tags, waivers, and classification for one
// scope, writes a summary to w, regenerates the matrix when the run is clean,
// and returns a non-nil error listing every integrity problem found.
func Check(cfg *Config, scope Scope, w io.Writer) error {
	reqs, catalogProblems, err := ParseCatalog(cfg, scope.Catalog)
	if err != nil {
		return err
	}
	if len(reqs) == 0 {
		return fmt.Errorf("no requirements found in %s", scope.Catalog)
	}
	tags, tagProblems, err := CollectTags(cfg, scope.Root)
	if err != nil {
		return err
	}
	waivers, waiverProblems, err := ParseWaivers(cfg, scope.Waivers)
	if err != nil {
		return err
	}
	problems := append(append(catalogProblems, tagProblems...), waiverProblems...)

	known := make(map[string]Requirement, len(reqs))
	catalogSeries := make(map[string]bool)
	for _, r := range reqs {
		known[r.ID] = r
		catalogSeries[cfg.seriesOf(r.ID)] = true
	}

	for _, r := range reqs {
		if r.Class == "" {
			problems = append(problems, fmt.Sprintf("%s: missing or unclassifiable Keyword line in catalog", r.ID))
		}
	}

	for _, id := range sortedTagKeys(tags) {
		// Only check tags whose ID series this catalog defines, so one scope
		// ignores another scope's tags.
		if !catalogSeries[cfg.seriesOf(id)] {
			continue
		}
		if _, ok := known[id]; !ok {
			refs := tags[id]
			problems = append(problems, fmt.Sprintf("%s: tagged by %s (%s) but not in the catalog", id, refs[0].Func, refs[0].File))
		}
	}

	validReason := make(map[string]bool, len(cfg.Waivers.Reasons))
	for _, r := range cfg.Waivers.Reasons {
		validReason[r] = true
	}
	waived := make(map[string]WaiverEntry, len(waivers))
	for _, wv := range waivers {
		if _, dup := waived[wv.ID]; dup {
			problems = append(problems, fmt.Sprintf("%s: duplicate waiver", wv.ID))
			continue
		}
		waived[wv.ID] = wv
		if _, ok := known[wv.ID]; !ok {
			problems = append(problems, fmt.Sprintf("%s: waived but not in the catalog", wv.ID))
		}
		if !validReason[wv.Reason] {
			problems = append(problems, fmt.Sprintf("%s: invalid waiver reason %q", wv.ID, wv.Reason))
		}
		if !wv.HasRationale {
			problems = append(problems, fmt.Sprintf("%s: waiver has no Rationale line", wv.ID))
		}
		if len(tags[wv.ID]) > 0 {
			problems = append(problems, fmt.Sprintf("%s: has both a waiver and tagged tests (%s)", wv.ID, tags[wv.ID][0].Func))
		}
	}

	covered := 0
	for _, r := range reqs {
		if len(tags[r.ID]) > 0 || waived[r.ID].ID != "" {
			covered++
		} else if scope.Strict {
			problems = append(problems, fmt.Sprintf("%s (%s, %s): no tagged test or waiver", r.ID, displayKeyword(r), r.Section))
		}
	}

	problems = append(problems, cfg.checkClassification(scope, reqs, known, tags)...)

	// Regenerate the matrix only from a consistent state.
	if scope.Out != "" && len(problems) == 0 {
		if err := cfg.writeMatrix(filepath.Join(scope.Root, scope.Out), reqs, tags, waived); err != nil {
			return err
		}
	}

	_, _ = fmt.Fprintf(w, "trace-check: %d requirements, %d covered (%d tagged, %d waived), %d uncovered\n",
		len(reqs), covered, countTagged(reqs, tags), len(waived), len(reqs)-covered)

	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintf(w, "  PROBLEM: %s\n", p)
		}
		return fmt.Errorf("%d problem(s)", len(problems))
	}
	return nil
}

// checkClassification enforces the classification policy: every requirement
// classified exactly once; the required Reason for values that demand it; a
// forbidden coverage class on a value is a stale classification; and, under
// strict, a value that requires a coverage class must carry one. Dormant when
// the classification file is absent (nil entries).
func (c *Config) checkClassification(scope Scope, reqs []Requirement, known map[string]Requirement, tags map[string][]TagRef) []string {
	entries, classProblems, err := ParseClassification(c, scope.Classification)
	if err != nil {
		return []string{fmt.Sprintf("classification: %v", err)}
	}
	if entries == nil {
		return nil // dormant
	}
	var problems []string
	problems = append(problems, classProblems...)

	byName := make(map[string]ClassValue, len(c.Classification.Values))
	var names []string
	for _, v := range c.Classification.Values {
		byName[v.Name] = v
		names = append(names, v.Name)
	}
	wantList := strings.Join(names, " or ")

	classOf := make(map[string]string, len(entries))
	for _, e := range entries {
		if _, dup := classOf[e.ID]; dup {
			problems = append(problems, fmt.Sprintf("%s: duplicate classification", e.ID))
			continue
		}
		classOf[e.ID] = e.Class
		if _, ok := known[e.ID]; !ok {
			problems = append(problems, fmt.Sprintf("%s: classified but not in the catalog", e.ID))
		}
		v, ok := byName[e.Class]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: invalid classification %q (want %s)", e.ID, e.Class, wantList))
			continue
		}
		if v.RequiresReason && !e.HasReason {
			problems = append(problems, fmt.Sprintf("%s: %s classification has no Reason line", e.ID, e.Class))
		}
	}
	for _, r := range reqs {
		class, ok := classOf[r.ID]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: not classified in %s", r.ID, filepath.Base(scope.Classification)))
			continue
		}
		v := byName[class]
		if v.ForbidsCoverageClass != "" && hasCoverageClass(tags[r.ID], v.ForbidsCoverageClass) {
			problems = append(problems, fmt.Sprintf("%s: classified %s but has a %s tag (stale classification)", r.ID, class, v.ForbidsCoverageClass))
		}
		if scope.Strict && v.StrictRequiresCoverageClass != "" && !hasCoverageClass(tags[r.ID], v.StrictRequiresCoverageClass) {
			problems = append(problems, fmt.Sprintf("%s (%s): %s but has no %s coverage", r.ID, r.Section, class, v.StrictRequiresCoverageClass))
		}
	}
	return problems
}

// hasCoverageClass reports whether any ref has the given coverage class.
func hasCoverageClass(refs []TagRef, class string) bool {
	for _, ref := range refs {
		if ref.Class == class {
			return true
		}
	}
	return false
}

func displayKeyword(r Requirement) string {
	if r.Keyword != "" {
		return r.Keyword
	}
	return r.Class
}

func countTagged(reqs []Requirement, tags map[string][]TagRef) int {
	n := 0
	for _, r := range reqs {
		if len(tags[r.ID]) > 0 {
			n++
		}
	}
	return n
}

func sortedTagKeys(m map[string][]TagRef) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
