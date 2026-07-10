package main

import (
	"flag"
	"os"
	"runtime/debug"
	"testing"
)

// update rewrites the committed SKILL.md from the current usage text. Run:
//
//	go test ./cmd/trace-check -run TestSkillDocInSync -update
var update = flag.Bool("update", false, "rewrite SKILL.md from the current usage text")

// skillDocPath is the generated reference, relative to this package dir.
const skillDocPath = "../../SKILL.md"

const skillHeader = "# trace-check — operating reference\n" +
	"\n" +
	"`trace-check` is a dialect-agnostic requirement-traceability checker. This\n" +
	"document is the complete operating manual — byte-for-byte identical to the\n" +
	"output of `trace-check -help` — so it can be read as a skill without running\n" +
	"the binary.\n" +
	"\n" +
	"GENERATED FILE — do not edit by hand. Refresh it after changing the CLI usage\n" +
	"text with:\n" +
	"\n" +
	"    go test ./cmd/trace-check -run TestSkillDocInSync -update\n"

// renderSkillDoc builds SKILL.md from the single source of truth (usageText),
// so the docs cannot drift from the actual --help output.
func renderSkillDoc() string {
	return skillHeader + "\n```text\n" + usageText + "```\n"
}

// TestSkillDocInSync fails when the committed SKILL.md no longer matches the
// CLI usage text — the docs-drift guard. With -update it regenerates the file.
func TestSkillDocInSync(t *testing.T) {
	want := renderSkillDoc()
	if *update {
		if err := os.WriteFile(skillDocPath, []byte(want), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(skillDocPath) // #nosec G304 -- fixed repo-relative path
	if err != nil {
		t.Fatalf("read SKILL.md (regenerate with `go test ./cmd/trace-check -run TestSkillDocInSync -update`): %v", err)
	}
	if string(got) != want {
		t.Errorf("SKILL.md is stale; regenerate with `go test ./cmd/trace-check -run TestSkillDocInSync -update`")
	}
}

func TestFormatBuildVersion(t *testing.T) {
	const revision = "0123456789abcdef0123456789abcdef01234567"
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.modified", Value: "false"},
		},
	}
	want := "trace-check version=v1.2.3 revision=" + revision + " modified=false"
	if got := formatBuildVersion(info); got != want {
		t.Fatalf("formatBuildVersion() = %q, want %q", got, want)
	}
}

func TestFormatBuildVersionModuleCacheBuildHasHonestUnknownVCS(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}
	want := "trace-check version=v1.2.3 revision=unknown modified=unknown"
	if got := formatBuildVersion(info); got != want {
		t.Fatalf("formatBuildVersion() = %q, want %q", got, want)
	}
}

func TestFormatBuildVersionUsesInjectedPinnedProvenance(t *testing.T) {
	oldVersion, oldRevision, oldModified := buildVersion, buildRevision, buildModified
	t.Cleanup(func() {
		buildVersion, buildRevision, buildModified = oldVersion, oldRevision, oldModified
	})
	buildVersion = "pinned"
	buildRevision = "0123456789abcdef0123456789abcdef01234567"
	buildModified = "false"
	info := &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}
	want := "trace-check version=pinned revision=" + buildRevision + " modified=false"
	if got := formatBuildVersion(info); got != want {
		t.Fatalf("formatBuildVersion() = %q, want %q", got, want)
	}
}
