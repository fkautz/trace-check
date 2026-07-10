package tracecheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadArchitecture reads a markdown architecture registry. An empty path
// returns (nil, nil) — architecture checking is dormant. The file format:
//
//	### Components          (heading text configurable)
//	- fault-in
//	- cas
//
//	### Invariants
//	- I-VERIFY — short title (title after " — " is optional)
//
// Headings match ## or ### with the configured section title. List items under
// each section become closed-vocabulary names for catalog field enumFrom.
func LoadArchitecture(cfg *Config, path string) (*Architecture, []string, error) {
	return loadArchitectureWithRead(cfg, path, os.ReadFile)
}

func loadArchitectureWithRead(cfg *Config, path string, readFile func(string) ([]byte, error)) (*Architecture, []string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil, nil
	}
	data, err := readFile(path) // #nosec G304 -- path is operator-supplied by the caller
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("architecture file %s not found", path)
		}
		return nil, nil, err
	}

	compSection := cfg.Architecture.ComponentSection
	if compSection == "" {
		compSection = "Components"
	}
	invSection := cfg.Architecture.InvariantSection
	if invSection == "" {
		invSection = "Invariants"
	}

	arch := &Architecture{
		componentSet: map[string]bool{},
		invariantSet: map[string]bool{},
	}
	var problems []string
	section := "" // "components" | "invariants" | ""
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if h, ok := headingTitle(trimmed); ok {
			switch h {
			case compSection:
				section = "components"
			case invSection:
				section = "invariants"
			default:
				section = ""
			}
			continue
		}
		if section == "" || !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		// Optional " — title" suffix; name is the part before it.
		name := item
		if i := strings.Index(item, " — "); i >= 0 {
			name = strings.TrimSpace(item[:i])
		}
		if name == "" {
			problems = append(problems, fmt.Sprintf("%s: empty list item under %s", filepath.Base(path), section))
			continue
		}
		switch section {
		case "components":
			if arch.componentSet[name] {
				problems = append(problems, fmt.Sprintf("%s: duplicate component %q", filepath.Base(path), name))
				continue
			}
			arch.Components = append(arch.Components, name)
			arch.componentSet[name] = true
		case "invariants":
			if arch.invariantSet[name] {
				problems = append(problems, fmt.Sprintf("%s: duplicate invariant %q", filepath.Base(path), name))
				continue
			}
			arch.Invariants = append(arch.Invariants, name)
			arch.invariantSet[name] = true
		}
	}
	return arch, problems, nil
}

// headingTitle returns the title of a ## or ### markdown heading.
func headingTitle(line string) (string, bool) {
	if strings.HasPrefix(line, "### ") {
		return strings.TrimSpace(strings.TrimPrefix(line, "### ")), true
	}
	if strings.HasPrefix(line, "## ") {
		return strings.TrimSpace(strings.TrimPrefix(line, "## ")), true
	}
	return "", false
}
