package tracecheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ParseCatalog extracts requirement entries from a catalog markdown file using
// the dialect in cfg. Malformed requirement headings (lines that look like a
// heading but whose ID is not well-formed) are returned as problems rather
// than silently skipped, so a typo cannot make a requirement vanish.
func ParseCatalog(cfg *Config, path string) ([]Requirement, []string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is operator-supplied
	if err != nil {
		return nil, nil, err
	}
	var reqs []Requirement
	var problems []string
	var cur *Requirement
	for _, line := range strings.Split(string(data), "\n") {
		if m := cfg.compiled.catalogHeading.FindStringSubmatch(line); m != nil {
			reqs = append(reqs, Requirement{ID: m[1], Title: m[2]})
			cur = &reqs[len(reqs)-1]
			cur.Class = cfg.classFromID(cur.ID)
			continue
		}
		if cfg.compiled.looseHeading.MatchString(line) {
			problems = append(problems, fmt.Sprintf("%s: malformed requirement heading %q", filepath.Base(path), line))
			cur = nil
			continue
		}
		if cur == nil {
			continue
		}
		if m := cfg.compiled.keywordLine.FindStringSubmatch(line); m != nil && cur.Keyword == "" {
			cur.Keyword = m[1]
			if cur.Class == "" {
				cur.Class = cfg.classFromKeyword(cur.Keyword)
			}
		}
		if m := cfg.compiled.sectionLine.FindStringSubmatch(line); m != nil && cur.Section == "" {
			if cfg.compiled.sectionRef != nil {
				if ref := cfg.compiled.sectionRef.FindString(m[1]); ref != "" {
					cur.Section = ref
					continue
				}
			}
			cur.Section = strings.TrimSpace(strings.SplitN(m[1], ";", 2)[0])
		}
	}
	return reqs, problems, nil
}
