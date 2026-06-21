package tracecheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// commentCollector discovers tags in non-Go source by scanning comments. It is
// language-agnostic: it reads files with the configured suffix line by line,
// accumulates a contiguous comment block, and attributes it to the next test
// item. An item is a test when a configured test marker (e.g. "#[test]")
// armed it, or when the item name has a configured prefix (e.g. "test_").
//
// Attribution mirrors a Go doc comment: the tag comment must be contiguous
// with the item (a blank line or unrelated code ends the block), so a stray
// "Verifies:" elsewhere in the file is not misattributed.
type commentCollector struct{ spec CollectorSpec }

// defaultItemPattern matches a function declaration and captures its name when
// the spec gives no NamePattern. It covers C/Rust/Go-style `fn`/`func` forms.
var defaultItemPattern = regexp.MustCompile(`(?:fn|func)\s+([A-Za-z0-9_]+)`)

func (c commentCollector) collect(cfg *Config, root string) ([]testSite, []string, error) {
	item := defaultItemPattern
	if c.spec.NamePattern != "" {
		item = regexp.MustCompile(c.spec.NamePattern) // Validate compiled it already
	}
	var sites []testSite
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if cfg.shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, c.spec.FileSuffix) {
			return nil
		}
		data, rerr := os.ReadFile(path) // #nosec G304 -- path is under the scan root
		if rerr != nil {
			return rerr
		}
		rel := relSlash(root, path)
		sites = append(sites, c.scan(cfg, rel, item, string(data))...)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return sites, nil, nil
}

// scan applies the comment-block state machine to one file's contents.
func (c commentCollector) scan(cfg *Config, rel string, item *regexp.Regexp, content string) []testSite {
	var sites []testSite
	var doc []string
	armed := false
	reset := func() { doc = nil; armed = false }

	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
			reset()
		case cfg.isCommentLine(t):
			doc = append(doc, line)
		case matchAnyPrefix(t, c.spec.TestMarkers):
			armed = true
		case item.MatchString(line):
			name := item.FindStringSubmatch(line)[1]
			isTest := armed || hasAnyPrefix(name, c.spec.FuncPrefixes)
			if isTest && len(doc) > 0 {
				sites = append(sites, testSite{File: rel, Func: name, Doc: doc})
			}
			reset()
		case strings.HasPrefix(t, "#[") || strings.HasPrefix(t, "@"):
			// Another attribute/annotation between the comment and the item
			// (e.g. #[should_panic], @pytest.mark): keep the block armed.
		default:
			// Unrelated code ends the block.
			reset()
		}
	}
	return sites
}

// isCommentLine reports whether a trimmed line begins with any configured
// comment marker.
func (c *Config) isCommentLine(trimmed string) bool {
	for _, m := range c.Tag.CommentMarkers {
		if strings.HasPrefix(trimmed, m) {
			return true
		}
	}
	return false
}
