package tracecheck

import (
	"fmt"
	"strings"
)

// testSite is one discovered test (function/item) with the comment lines that
// may carry tags. Collectors produce these; CollectTags applies the shared tag
// semantics (parse, dedupe, one-per-test, coverage class) uniformly so the
// rules live in exactly one place regardless of source language.
type testSite struct {
	File string // slash-normalized path relative to the scan root
	Func string
	Doc  []string // comment lines to scan for tags
}

// collector discovers test sites of one language under root.
type collector interface {
	collect(cfg *Config, root string) ([]testSite, []string, error)
}

// newCollector builds the collector for a CollectorSpec. Validate has already
// rejected unknown langs, so the default panic is unreachable in practice.
func newCollector(spec CollectorSpec) collector {
	switch spec.Lang {
	case "go":
		return goCollector{spec}
	case "comment":
		return commentCollector{spec}
	default:
		return nil
	}
}

// CollectTags runs every configured collector over root and returns the tag
// map (requirement ID → refs) and any integrity problems (malformed tag lines,
// tests tagging more than one requirement).
func CollectTags(cfg *Config, root string) (map[string][]TagRef, []string, error) {
	tags := map[string][]TagRef{}
	var problems []string
	for _, spec := range cfg.Tag.Collectors {
		c := newCollector(spec)
		if c == nil {
			return nil, nil, fmt.Errorf("no collector for lang %q", spec.Lang)
		}
		sites, probs, err := c.collect(cfg, root)
		if err != nil {
			return nil, nil, err
		}
		problems = append(problems, probs...)
		for _, s := range sites {
			class := cfg.coverageClass(s.File, s.Func)
			seen := map[string]bool{}
			var fnIDs []string
			for _, line := range s.Doc {
				ids, perr := cfg.parseTagLine(line)
				if perr != "" {
					problems = append(problems, fmt.Sprintf("%s (%s): %s", s.Func, s.File, perr))
				}
				for _, id := range ids {
					tags[id] = append(tags[id], TagRef{File: s.File, Func: s.Func, Class: class})
					if !seen[id] {
						seen[id] = true
						fnIDs = append(fnIDs, id)
					}
				}
			}
			// One requirement per test: a failing test must attribute to
			// exactly one catalog entry.
			if len(fnIDs) > 1 {
				problems = append(problems, fmt.Sprintf("%s (%s): tags %d requirements (%s); one requirement per test — split the test or use a covered-by waiver", s.Func, s.File, len(fnIDs), strings.Join(fnIDs, ", ")))
			}
		}
	}
	return tags, problems, nil
}

// parseTagLine extracts requirement IDs from one comment line. It returns the
// IDs and an empty problem for a well-formed tag line, no IDs for a line
// without the tag keyword, and a problem description for a malformed tag line.
func (c *Config) parseTagLine(line string) (ids []string, problem string) {
	s := strings.TrimSpace(line)
	for _, marker := range c.Tag.CommentMarkers {
		s = strings.TrimSpace(strings.TrimPrefix(s, marker))
	}
	idx := strings.Index(s, c.Tag.Keyword)
	if idx < 0 {
		return nil, ""
	}
	if idx != 0 {
		return nil, fmt.Sprintf("malformed tag line (text before %q): %q", c.Tag.Keyword, strings.TrimSpace(line))
	}
	payload := strings.TrimSpace(s[len(c.Tag.Keyword):])
	if payload == "" {
		return nil, fmt.Sprintf("malformed tag line (empty ID list): %q", strings.TrimSpace(line))
	}
	for _, tok := range strings.Split(payload, ",") {
		tok = strings.TrimSpace(tok)
		if !c.compiled.fullID.MatchString(tok) {
			return nil, fmt.Sprintf("malformed tag line (bad ID %q): %q", tok, strings.TrimSpace(line))
		}
		ids = append(ids, tok)
	}
	return ids, ""
}

// coverageClass assigns a coverage class to a tag from its file and test name
// using Config.Coverage rules; the first matching rule wins, else the default.
func (c *Config) coverageClass(file, fn string) string {
	for _, r := range c.Coverage.Rules {
		if matchAnyPrefix(file, r.PathPrefixes) && matchAnyPrefix(fn, r.FuncPrefixes) {
			return r.Class
		}
	}
	return c.Coverage.Default
}

// matchAnyPrefix reports whether s has any of the prefixes; an empty prefix
// list means "no constraint" (always matches).
func matchAnyPrefix(s string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
