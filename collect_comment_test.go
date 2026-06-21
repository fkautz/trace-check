package tracecheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// rustConfig is a Rust dialect: a non-default ID grammar, /// doc comments,
// #[test] attributes marking test items, and no black-box coverage layer.
func rustConfig(t *testing.T) *Config {
	t.Helper()
	cfg := Default()
	cfg.IDGrammar = IDGrammar{
		Pattern:       `RUST-\d{3}`,
		HeadingPrefix: "RUST-",
		SeriesPattern: `^(RUST)-`,
	}
	cfg.Tag = TagConfig{
		Keyword:        "Verifies:",
		CommentMarkers: []string{"///", "//", "/*", "*/", "*"},
		Collectors: []CollectorSpec{{
			Lang:        "comment",
			FileSuffix:  ".rs",
			TestMarkers: []string{"#[test]", "#[tokio::test]"},
			NamePattern: `fn\s+([A-Za-z0-9_]+)`,
		}},
	}
	cfg.Coverage = CoverageConfig{Default: "unit"}
	cfg.Classification = ClassConfig{
		ClassField:  "Class",
		ReasonField: "Reason",
		Values: []ClassValue{
			{Name: "checked"},
			{Name: "unchecked", RequiresReason: true},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("rustConfig invalid: %v", err)
	}
	return &cfg
}

func collectRust(t *testing.T, src string) (map[string][]TagRef, []string) {
	t.Helper()
	root := t.TempDir()
	mustWriteFile(t, root, "src/lib.rs", src)
	tags, problems, err := CollectTags(rustConfig(t), root)
	if err != nil {
		t.Fatal(err)
	}
	return tags, problems
}

// TestRustDocCommentAboveTestAttr: a /// doc comment carrying a tag, sitting
// above a #[test] attribute and its fn, is attributed to that test.
func TestRustDocCommentAboveTestAttr(t *testing.T) {
	tags, problems := collectRust(t, `
/// Verifies: RUST-001
#[test]
fn accepts_valid_token() {
    assert!(true);
}
`)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	refs := tags["RUST-001"]
	if len(refs) != 1 || refs[0].Func != "accepts_valid_token" {
		t.Fatalf("tags[RUST-001] = %v, want one ref from accepts_valid_token", refs)
	}
	if refs[0].File != "src/lib.rs" {
		t.Errorf("ref file = %q, want src/lib.rs", refs[0].File)
	}
}

// TestRustLineCommentAndTokioAttr: a // line comment and the #[tokio::test]
// marker also attribute correctly.
func TestRustLineCommentAndTokioAttr(t *testing.T) {
	tags, problems := collectRust(t, `
// Verifies: RUST-002
#[tokio::test]
async fn streams_ok() {}
`)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(tags["RUST-002"]) != 1 || tags["RUST-002"][0].Func != "streams_ok" {
		t.Errorf("tags[RUST-002] = %v, want one ref from streams_ok", tags["RUST-002"])
	}
}

// TestRustNonTestFnIgnored: a tag above a plain (non-#[test]) fn is not
// counted — only test items carry coverage.
func TestRustNonTestFnIgnored(t *testing.T) {
	tags, problems := collectRust(t, `
/// Verifies: RUST-003
fn helper() {}
`)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(tags) != 0 {
		t.Errorf("tag on a non-test fn counted: %v", tags)
	}
}

// TestRustStrayTagNotMisattributed: a tag comment separated from the test by a
// blank line (so it is not the test's doc comment) is not attributed — it
// mirrors Go's contiguous-doc-comment rule.
func TestRustStrayTagNotMisattributed(t *testing.T) {
	tags, _ := collectRust(t, `
// Verifies: RUST-004

let x = 1; // unrelated code ends the block

#[test]
fn unrelated() {}
`)
	if len(tags) != 0 {
		t.Errorf("stray tag misattributed: %v", tags)
	}
}

// TestRustOnePerTestEnforced: a single test tagging two requirements is an
// integrity problem, exactly like the Go collector.
func TestRustOnePerTestEnforced(t *testing.T) {
	tags, problems := collectRust(t, `
/// Verifies: RUST-001
/// Verifies: RUST-002
#[test]
fn two_reqs() {}
`)
	if len(problems) != 1 || !strings.Contains(problems[0], "two_reqs") {
		t.Fatalf("problems = %v, want one naming two_reqs", problems)
	}
	// Both still count toward coverage while the split is pending.
	if len(tags["RUST-001"]) != 1 || len(tags["RUST-002"]) != 1 {
		t.Errorf("multi-tag refs miscounted: %v / %v", tags["RUST-001"], tags["RUST-002"])
	}
}

// TestRustMalformedTagReported: a malformed ID in a Rust tag is reported the
// same way as in Go (shared parseTagLine).
func TestRustMalformedTagReported(t *testing.T) {
	_, problems := collectRust(t, `
/// Verifies: RUST-1
#[test]
fn bad_id() {}
`)
	if len(problems) != 1 || !strings.Contains(problems[0], "malformed tag line") {
		t.Errorf("problems = %v, want one malformed-tag problem", problems)
	}
}

// TestRustEndToEnd: a complete Rust-dialect scope — catalog, classification,
// and #[test] tags — reconciles cleanly and generates a matrix, proving the
// dialect is fully swappable via config alone.
func TestRustEndToEnd(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, root, "spec/requirements.md", `# Rust catalog

### RUST-001 — Accepts valid input
- Section: §2.1
- Keyword: MUST | Actor: Lib

### RUST-002 — Streams output
- Section: §2.2
- Keyword: SHOULD | Actor: Lib
`)
	mustWriteFile(t, root, "spec/classification.md", `# Classification

### RUST-001
- Class: checked

### RUST-002
- Class: checked
`)
	mustWriteFile(t, root, "src/lib.rs", `
/// Verifies: RUST-001
#[test]
fn accepts_valid() {}

/// Verifies: RUST-002
#[test]
fn streams() {}
`)
	var out strings.Builder
	err := Check(rustConfig(t), Scope{
		Root:           root,
		Catalog:        filepath.Join(root, "spec", "requirements.md"),
		Classification: filepath.Join(root, "spec", "classification.md"),
		Waivers:        filepath.Join(root, "spec", "waivers.md"),
		Out:            "docs/traceability.md",
	}, &out)
	if err != nil {
		t.Fatalf("Check: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "2 requirements, 2 covered") {
		t.Errorf("summary = %q", out.String())
	}
}
