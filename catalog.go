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
//
// Optional CatalogConfig.Fields are stored on Requirement.Meta. Required and
// enum validation for those fields is performed later in Check (once an
// architecture registry, if any, is loaded).
func ParseCatalog(cfg *Config, path string) ([]Requirement, []string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is operator-supplied
	if err != nil {
		return nil, nil, err
	}
	var reqs []Requirement
	var problems []string
	seen := map[string]bool{}
	var cur *Requirement
	for _, line := range strings.Split(string(data), "\n") {
		if m := cfg.compiled.catalogHeading.FindStringSubmatch(line); m != nil {
			if seen[m[1]] {
				problems = append(problems, fmt.Sprintf("%s: duplicate requirement ID %s", filepath.Base(path), m[1]))
			}
			seen[m[1]] = true
			reqs = append(reqs, Requirement{ID: m[1], Title: m[2], Meta: map[string]string{}})
			cur = &reqs[len(reqs)-1]
			cur.Class = cfg.classFromID(cur.ID)
			continue
		}
		if cfg.isHeadingCandidate(line) {
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
		for name, re := range cfg.compiled.metaFieldLines {
			if m := re.FindStringSubmatch(line); m != nil {
				if _, exists := cur.Meta[name]; !exists {
					cur.Meta[name] = strings.TrimSpace(m[1])
				}
			}
		}
	}
	return reqs, problems, nil
}

// validateCatalogMeta checks required/enum/enumFrom constraints on configured
// catalog fields. arch may be nil when architecture is dormant; enumFrom fields
// then produce problems if any requirement sets them (or if they are required).
func (c *Config) validateCatalogMeta(reqs []Requirement, arch *Architecture) []string {
	if len(c.Catalog.Fields) == 0 {
		return nil
	}
	var problems []string
	for _, r := range reqs {
		for _, f := range c.Catalog.Fields {
			val := r.MetaValue(f.Name)
			if val == "" {
				if f.Required {
					problems = append(problems, fmt.Sprintf("%s: missing required catalog field %s", r.ID, f.Name))
				}
				continue
			}
			allowed, err := c.fieldAllowedValues(f, arch)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: %s: %v", r.ID, f.Name, err))
				continue
			}
			if allowed != nil && !allowed[val] {
				problems = append(problems, fmt.Sprintf("%s: invalid %s %q", r.ID, f.Name, val))
			}
		}
	}
	return problems
}

// fieldAllowedValues returns a set of allowed values, or nil if any value is OK.
func (c *Config) fieldAllowedValues(f CatalogField, arch *Architecture) (map[string]bool, error) {
	if f.EnumFrom != "" {
		if arch == nil {
			return nil, fmt.Errorf("field uses enumFrom %s but no architecture registry is loaded", f.EnumFrom)
		}
		switch f.EnumFrom {
		case "architecture.components":
			return mapFromSlice(arch.Components), nil
		case "architecture.invariants":
			return mapFromSlice(arch.Invariants), nil
		default:
			return nil, fmt.Errorf("unknown enumFrom %q", f.EnumFrom)
		}
	}
	if len(f.Enum) == 0 {
		return nil, nil
	}
	return mapFromSlice(f.Enum), nil
}

func mapFromSlice(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
