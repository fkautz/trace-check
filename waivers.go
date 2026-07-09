package tracecheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ParseWaivers extracts waiver entries from a waivers markdown file. An absent
// file returns nil (no waivers). Malformed headings are returned as problems.
//
// When CoversField is configured, a "- Covers: <ID>" line is captured as the
// structured covered-by target. Validation of the target happens in Check.
func ParseWaivers(cfg *Config, path string) ([]WaiverEntry, []string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is operator-supplied
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var waivers []WaiverEntry
	var problems []string
	var cur *WaiverEntry
	for _, line := range strings.Split(string(data), "\n") {
		if m := cfg.compiled.plainHeading.FindStringSubmatch(line); m != nil {
			waivers = append(waivers, WaiverEntry{ID: m[1]})
			cur = &waivers[len(waivers)-1]
			continue
		}
		if cfg.compiled.looseHeading.MatchString(line) {
			problems = append(problems, fmt.Sprintf("%s: malformed waiver heading %q", filepath.Base(path), line))
			cur = nil
			continue
		}
		if cur == nil {
			continue
		}
		if m := cfg.compiled.reasonField.FindStringSubmatch(line); m != nil && cur.Reason == "" {
			// The whole remainder is the reason; trailing junk makes it invalid
			// rather than being silently dropped.
			cur.Reason = strings.TrimSpace(m[1])
		}
		if cfg.compiled.coversField != nil {
			if m := cfg.compiled.coversField.FindStringSubmatch(line); m != nil && cur.Covers == "" {
				cur.Covers = strings.TrimSpace(m[1])
			}
		}
		if strings.HasPrefix(line, cfg.compiled.rationalePrefix) {
			cur.HasRationale = true
		}
	}
	return waivers, problems, nil
}
