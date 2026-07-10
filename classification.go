package tracecheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ParseClassification extracts entries from a classification markdown file. An
// absent file returns nil entries (the feature is dormant). Malformed headings
// are returned as problems.
func ParseClassification(cfg *Config, path string) ([]ClassEntry, []string, error) {
	return parseClassificationWithRead(cfg, path, os.ReadFile)
}

func parseClassificationWithRead(cfg *Config, path string, readFile func(string) ([]byte, error)) ([]ClassEntry, []string, error) {
	data, err := readFile(path) // #nosec G304 -- path is operator-supplied by the caller
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	// A successfully read but empty/prose-only file is active and therefore
	// missing every catalog requirement. Only an absent file is dormant.
	entries := make([]ClassEntry, 0)
	var problems []string
	var cur *ClassEntry
	for _, line := range strings.Split(string(data), "\n") {
		if m := cfg.compiled.plainHeading.FindStringSubmatch(line); m != nil {
			entries = append(entries, ClassEntry{ID: m[1]})
			cur = &entries[len(entries)-1]
			continue
		}
		if cfg.isHeadingCandidate(line) {
			problems = append(problems, fmt.Sprintf("%s: malformed heading %q", filepath.Base(path), line))
			cur = nil
			continue
		}
		if cur == nil {
			continue
		}
		if rest, ok := strings.CutPrefix(line, cfg.compiled.classClassField); ok && cur.Class == "" {
			cur.Class = strings.TrimSpace(rest)
		}
		if strings.HasPrefix(line, cfg.compiled.classReasonPrefix) {
			cur.HasReason = true
		}
	}
	return entries, problems, nil
}
