package tracecheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
)

// goCollector discovers tags in Go *_test.go files via the AST. It considers
// only Test*/Fuzz*/Example* (configurable) *functions* — methods cannot be
// tests, so a tag on a test-shaped method is ignored.
type goCollector struct{ spec CollectorSpec }

func (g goCollector) collect(cfg *Config, root string) ([]testSite, []string, error) {
	var sites []testSite
	var problems []string
	fset := token.NewFileSet()
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
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", path, perr)
		}
		rel := relSlash(root, path)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Doc == nil || fn.Recv != nil {
				continue
			}
			if !hasAnyPrefix(fn.Name.Name, g.spec.FuncPrefixes) {
				continue
			}
			var doc []string
			for _, c := range fn.Doc.List {
				doc = append(doc, strings.Split(c.Text, "\n")...)
			}
			sites = append(sites, testSite{File: rel, Func: fn.Name.Name, Doc: doc})
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return sites, problems, nil
}

// shouldSkipDir reports whether a directory name is in Config.SkipDirs.
func (c *Config) shouldSkipDir(name string) bool {
	for _, s := range c.SkipDirs {
		if name == s {
			return true
		}
	}
	return false
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// relSlash returns path relative to root in slash form, falling back to the
// original path if it cannot be made relative.
func relSlash(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
