package tracecheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRustFixtureViaLoadedConfig drives the committed testdata/rust-project
// example through the real LoadConfig + Check path: a non-Go project with its
// own ID grammar, comment-scanned #[test] tags, a classification, and a waiver
// reconciles cleanly. This is the end-to-end proof that the dialect is
// swappable purely via the published JSON config — no code change.
func TestRustFixtureViaLoadedConfig(t *testing.T) {
	const root = "testdata/rust-project"
	cfg, err := LoadConfig(filepath.Join(root, "tracecheck.json"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	var out strings.Builder
	err = Check(&cfg, Scope{
		Root:           root,
		Catalog:        filepath.Join(root, "spec", "requirements.md"),
		Classification: filepath.Join(root, "spec", "classification.md"),
		Waivers:        filepath.Join(root, "spec", "waivers.md"),
		Out:            "", // do not write into the committed fixture
		Strict:         true,
	}, &out)
	if err != nil {
		t.Fatalf("Check on rust fixture: %v\n%s", err, out.String())
	}
	// RUST-001/002 tagged via #[test], RUST-003 waived → 3 covered, 0 uncovered
	// even under -strict.
	if !strings.Contains(out.String(), "3 requirements, 3 covered") {
		t.Errorf("summary = %q, want 3 requirements, 3 covered", out.String())
	}
}
