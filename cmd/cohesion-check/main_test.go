package main

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"
)

func TestFormatBuildVersion(t *testing.T) {
	const revision = "0123456789abcdef0123456789abcdef01234567"
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.modified", Value: "false"},
		},
	}
	want := "cohesion-check version=v1.2.3 revision=" + revision + " modified=false"
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
	want := "cohesion-check version=pinned revision=" + buildRevision + " modified=false"
	if got := formatBuildVersion(&debug.BuildInfo{}); got != want {
		t.Fatalf("formatBuildVersion() = %q, want %q", got, want)
	}
}

func TestRunRejectsUnexpectedArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"surprise"}, &stdout, &stderr, nil); code != 2 {
		t.Fatalf("run() = %d, want 2; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "unexpected argument") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunVersionAndHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-version"}, &stdout, &stderr, &debug.BuildInfo{}); code != 0 {
		t.Fatalf("version run() = %d; stderr=%q", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "cohesion-check version=") {
		t.Fatalf("version output = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"help"}, &stdout, &stderr, nil); code != 0 {
		t.Fatalf("help run() = %d; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"RECEIPT SCHEMA", "NON-GOALS", "-warnings-as-errors", "-require-component", "-goos"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("help output missing %q", want)
		}
	}
}

func TestRunRejectsUnknownOutputFormat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-format", "yaml"}, &stdout, &stderr, nil); code != 2 {
		t.Fatalf("run() = %d, want 2; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), `invalid -format "yaml"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
