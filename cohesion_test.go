package tracecheck

import (
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cohesionCatalog = `# Catalog

### REQ-CORE-001 — First island
- Section: §1
- Keyword: MUST
- Component: alpha

### REQ-CORE-002 — Second island routed elsewhere
- Section: §2
- Keyword: MUST
- Component: beta

### REQ-CORE-003 — Not implemented
- Section: §3
- Keyword: MUST
- Component: alpha
`

const cohesionProduction = `package alpha

func First() {}

type Service struct{}

func (*Service) Second() {}

func Compose() {}

func Legacy() { Compose() }

func Helper() {}

func unexported() {}
`

const cohesionTests = `package alpha

import "testing"

// TestFirst proves the first island.
//
// Verifies: REQ-CORE-001
func TestFirst(t *testing.T) {}

// TestSecond proves the second island.
//
// Verifies: REQ-CORE-002
func TestSecond(t *testing.T) {}

func TestGolden(t *testing.T) {}

// TestTaggedGolden is deliberately tagged for the warning test.
//
// Verifies: REQ-CORE-001
func TestTaggedGolden(t *testing.T) {}
`

const validCohesionReceipt = `{
  "schemaVersion": 1,
  "goBuild": {
    "goos": "linux",
    "goarch": "amd64",
    "goVersion": "go1.25",
    "compiler": "gc",
    "cgoEnabled": false,
    "tags": [],
    "toolTags": []
  },
  "components": [
    {
      "component": "alpha",
      "status": "cohesive",
      "packages": ["internal/alpha"],
      "islands": [
        {
          "name": "first",
          "symbols": [{"package": "internal/alpha", "name": "First"}],
          "evidence": [
            {
              "requirement": "REQ-CORE-001",
              "tests": [{"file": "internal/alpha/alpha_test.go", "func": "TestFirst"}]
            }
          ],
          "invariants": ["I-VERIFY"]
        },
        {
          "name": "second",
          "symbols": [{"package": "internal/alpha", "name": "Service.Second"}],
          "evidence": [
            {
              "requirement": "REQ-CORE-002",
              "tests": [{"file": "internal/alpha/alpha_test.go", "func": "TestSecond"}]
            }
          ]
        }
      ],
      "operations": [
        {
          "name": "compose",
          "entrypoint": {"package": "internal/alpha", "name": "Compose"},
          "publishPoint": "successful return",
          "stages": ["first", "second"],
          "goldenPathTest": {"file": "internal/alpha/alpha_test.go", "func": "TestGolden"},
          "invariants": ["I-VERIFY"],
          "delegates": [{"package": "internal/alpha", "name": "Legacy"}],
          "retired": [{"package": "internal/alpha", "name": "Gone"}]
        }
      ],
      "primitives": [
        {
          "symbol": {"package": "internal/alpha", "name": "First"},
          "rationale": "leaf primitive retained for focused callers"
        },
        {
          "symbol": {"package": "internal/alpha", "name": "Service.Second"},
          "rationale": "leaf primitive retained for focused callers"
        },
        {
          "symbol": {"package": "internal/alpha", "name": "Helper"},
          "rationale": "diagnostic primitive"
        }
      ]
    }
  ]
}`

func writeCohesionRepo(t *testing.T, receipt string) (*Config, CohesionScope) {
	t.Helper()
	root := t.TempDir()
	mustWriteFile(t, root, "spec/requirements.md", cohesionCatalog)
	mustWriteFile(t, root, "docs/architecture.md", `# Architecture

## Components

- alpha
- beta

## Invariants

- I-VERIFY — publish only after verification
`)
	mustWriteFile(t, root, "internal/alpha/alpha.go", cohesionProduction)
	mustWriteFile(t, root, "internal/alpha/alpha_test.go", cohesionTests)
	mustWriteFile(t, root, "cohesion.json", receipt)

	cfg := Default()
	cfg.Catalog.Fields = []CatalogField{{
		Name:     "Component",
		Required: true,
		EnumFrom: "architecture.components",
	}}
	cfg.Architecture = ArchitectureConfig{
		Path:             "docs/architecture.md",
		ComponentSection: "Components",
		InvariantSection: "Invariants",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate config: %v", err)
	}
	return &cfg, CohesionScope{
		Root:    root,
		Catalog: "spec/requirements.md",
		Receipt: "cohesion.json",
	}
}

func TestCheckCohesionAcceptsTaggedIslandsAcrossRoutingComponents(t *testing.T) {
	cfg, scope := writeCohesionRepo(t, validCohesionReceipt)
	var out strings.Builder
	if err := CheckCohesion(cfg, scope, &out); err != nil {
		t.Fatalf("CheckCohesion: %v\n%s", err, out.String())
	}
	if got := out.String(); !strings.Contains(got, "1 component, 2 islands, 1 operation") ||
		!strings.Contains(got, "Go build linux/amd64, cgo=false") ||
		!strings.Contains(got, "receipt valid (0 advisories)") {
		t.Fatalf("unexpected report:\n%s", got)
	}
}

func TestCheckCohesionUsesReceiptPinnedGoBuildContext(t *testing.T) {
	receipt := strings.Replace(validCohesionReceipt, `"name": "Compose"`, `"name": "LinuxCompose"`, 1)
	cfg, scope := writeCohesionRepo(t, receipt)
	mustWriteFile(t, scope.Root, "internal/alpha/platform_linux.go", `package alpha

func LinuxCompose() {}
`)
	mustWriteFile(t, scope.Root, "internal/alpha/platform_darwin.go", `package alpha

func DarwinCompose() {}
`)
	var out strings.Builder
	if err := CheckCohesion(cfg, scope, &out); err != nil {
		t.Fatalf("receipt-pinned Linux symbol rejected: %v\n%s", err, out.String())
	}

	scope.GOOS = "darwin"
	out.Reset()
	if err := CheckCohesion(cfg, scope, &out); err == nil {
		t.Fatalf("cross-target override accepted Linux-only entrypoint:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "symbol LinuxCompose does not exist") ||
		!strings.Contains(out.String(), "Go build darwin/amd64") {
		t.Fatalf("unexpected cross-target report:\n%s", out.String())
	}
}

func TestCheckCohesionRejectsUnsupportedGoTarget(t *testing.T) {
	receipt := strings.Replace(validCohesionReceipt, `"goos": "linux"`, `"goos": "linuz"`, 1)
	cfg, scope := writeCohesionRepo(t, receipt)
	var out strings.Builder
	err := CheckCohesion(cfg, scope, &out)
	var scopeErr *ScopeError
	if !errors.As(err, &scopeErr) || !strings.Contains(err.Error(), `target "linuz/amd64" is not supported`) {
		t.Fatalf("CheckCohesion() error = %v, want unsupported-target ScopeError", err)
	}
}

func TestCheckCohesionRejectsUnsupportedGoSchemaContext(t *testing.T) {
	tests := []struct {
		name        string
		old         string
		replacement string
		want        string
	}{
		{"future Go release", `"goVersion": "go1.25"`, `"goVersion": "go1.26"`, `want go1.25`},
		{"unknown compiler", `"compiler": "gc"`, `"compiler": "fictional"`, `want gc`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := strings.Replace(validCohesionReceipt, tt.old, tt.replacement, 1)
			cfg, scope := writeCohesionRepo(t, receipt)
			var out strings.Builder
			err := CheckCohesion(cfg, scope, &out)
			var scopeErr *ScopeError
			if !errors.As(err, &scopeErr) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("CheckCohesion() error = %v, want ScopeError containing %q", err, tt.want)
			}
		})
	}
}

func TestCheckCohesionPinsGoReleaseTags(t *testing.T) {
	receipt := strings.Replace(validCohesionReceipt,
		`"name": "Compose"`, `"name": "FutureCompose"`, 1)
	cfg, scope := writeCohesionRepo(t, receipt)
	mustWriteFile(t, scope.Root, "internal/alpha/future.go", `//go:build go1.26

package alpha

func FutureCompose() {}
`)
	var out strings.Builder
	if err := CheckCohesion(cfg, scope, &out); err == nil {
		t.Fatalf("future Go release symbol accepted by go1.25 receipt:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "symbol FutureCompose does not exist") {
		t.Fatalf("unexpected report:\n%s", out.String())
	}
}

func TestCheckCohesionReportsSemanticFindings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(string) string
		want   string
	}{
		{
			name: "unknown component",
			mutate: func(s string) string {
				return strings.Replace(s, `"component": "alpha"`, `"component": "unknown"`, 1)
			},
			want: `component "unknown" is not registered`,
		},
		{
			name: "unknown invariant",
			mutate: func(s string) string {
				return strings.Replace(s, `"I-VERIFY"`, `"I-UNKNOWN"`, 1)
			},
			want: `invariant "I-UNKNOWN" is not registered`,
		},
		{
			name: "untagged requirement",
			mutate: func(s string) string {
				return strings.Replace(s, `"REQ-CORE-002"`, `"REQ-CORE-003"`, 1)
			},
			want: `REQ-CORE-003 has no tagged test evidence`,
		},
		{
			name: "unrelated evidence test",
			mutate: func(s string) string {
				return strings.Replace(s, `"func": "TestSecond"`, `"func": "TestGolden"`, 1)
			},
			want: `TestGolden is not tagged for REQ-CORE-002`,
		},
		{
			name: "missing symbol",
			mutate: func(s string) string {
				return strings.Replace(s, `"name": "Service.Second"`, `"name": "Service.Missing"`, 1)
			},
			want: `symbol Service.Missing does not exist`,
		},
		{
			name: "missing golden test",
			mutate: func(s string) string {
				return strings.Replace(s, `"func": "TestGolden"`, `"func": "TestMissing"`, 1)
			},
			want: `golden-path test TestMissing does not exist`,
		},
		{
			name: "duplicate stage",
			mutate: func(s string) string {
				return strings.Replace(s, `["first", "second"]`, `["first", "first"]`, 1)
			},
			want: `duplicates stage "first"`,
		},
		{
			name: "retired symbol still present",
			mutate: func(s string) string {
				return strings.Replace(s, `"name": "Gone"`, `"name": "Legacy"`, 1)
			},
			want: `retired symbol Legacy still exists`,
		},
		{
			name: "empty retired symbol",
			mutate: func(s string) string {
				return strings.Replace(s, `"name": "Gone"`, `"name": ""`, 1)
			},
			want: `retired symbol name "" is empty or invalid`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, scope := writeCohesionRepo(t, tt.mutate(validCohesionReceipt))
			var out strings.Builder
			err := CheckCohesion(cfg, scope, &out)
			if err == nil {
				t.Fatalf("CheckCohesion accepted invalid receipt:\n%s", out.String())
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Fatalf("report = %q, want %q", out.String(), tt.want)
			}
		})
	}
}

func TestCheckCohesionRejectsCopiedIslandEvidence(t *testing.T) {
	receipt := strings.Replace(validCohesionReceipt, `"REQ-CORE-002"`, `"REQ-CORE-001"`, 1)
	receipt = strings.Replace(receipt, `"func": "TestSecond"`, `"func": "TestFirst"`, 1)
	cfg, scope := writeCohesionRepo(t, receipt)
	var out strings.Builder
	if err := CheckCohesion(cfg, scope, &out); err == nil {
		t.Fatalf("copied island evidence accepted:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `tagged evidence REQ-CORE-001 at internal/alpha/alpha_test.go:TestFirst is reused by islands "first" and "second"`) {
		t.Fatalf("unexpected report:\n%s", out.String())
	}
}

func TestCheckCohesionAdvisoriesCanBeRatchetEnforced(t *testing.T) {
	receipt := strings.Replace(validCohesionReceipt,
		`"status": "cohesive",`,
		`"status": "compose-needed",
      "rationale": "two public islands still require caller sequencing",`, 1)
	receipt = strings.Replace(receipt,
		`,
        {
          "symbol": {"package": "internal/alpha", "name": "Helper"},
          "rationale": "diagnostic primitive"
        }`, "", 1)

	cfg, scope := writeCohesionRepo(t, receipt)
	var out strings.Builder
	if err := CheckCohesion(cfg, scope, &out); err != nil {
		t.Fatalf("advisory run failed: %v\n%s", err, out.String())
	}
	if got := out.String(); !strings.Contains(got, "status is compose-needed") ||
		!strings.Contains(got, "unclassified exported callable Helper") {
		t.Fatalf("missing advisories:\n%s", got)
	}

	scope.WarningsAsErrors = true
	out.Reset()
	if err := CheckCohesion(cfg, scope, &out); err == nil {
		t.Fatalf("warnings-as-errors accepted advisories:\n%s", out.String())
	}
}

func TestCheckCohesionWarnsWhenGoldenPathTestIsRequirementTagged(t *testing.T) {
	receipt := strings.Replace(validCohesionReceipt, `"func": "TestGolden"`, `"func": "TestTaggedGolden"`, 1)
	cfg, scope := writeCohesionRepo(t, receipt)
	var out strings.Builder
	if err := CheckCohesion(cfg, scope, &out); err != nil {
		t.Fatalf("tagged golden path is advisory by default: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "golden-path test is requirement-tagged") {
		t.Fatalf("missing tagged-golden advisory:\n%s", out.String())
	}
}

func TestCheckCohesionRejectsNonRunnableGoTests(t *testing.T) {
	tests := []struct {
		name        string
		old         string
		replacement string
		want        string
	}{
		{
			name:        "golden path wrong signature",
			old:         `func TestGolden(t *testing.T) {}`,
			replacement: `func TestGolden() {}`,
			want:        "golden-path test: TestGolden must have signature func(*testing.T)",
		},
		{
			name:        "evidence wrong signature",
			old:         `func TestFirst(t *testing.T) {}`,
			replacement: `func TestFirst() {}`,
			want:        "evidence test TestFirst is not runnable: TestFirst must have signature func(*testing.T)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, scope := writeCohesionRepo(t, validCohesionReceipt)
			path := filepath.Join(scope.Root, "internal/alpha/alpha_test.go")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			changed := strings.Replace(string(data), tt.old, tt.replacement, 1)
			if changed == string(data) {
				t.Fatalf("fixture mutation did not apply")
			}
			if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			if err := CheckCohesion(cfg, scope, &out); err == nil {
				t.Fatalf("non-runnable test accepted:\n%s", out.String())
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Fatalf("report = %q, want %q", out.String(), tt.want)
			}
		})
	}
}

func TestCheckCohesionRejectsBuildExcludedGoldenPathTest(t *testing.T) {
	cfg, scope := writeCohesionRepo(t, validCohesionReceipt)
	receiptPath := filepath.Join(scope.Root, "cohesion.json")
	receipt, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	receipt = []byte(strings.Replace(string(receipt),
		`"file": "internal/alpha/alpha_test.go", "func": "TestGolden"`,
		`"file": "internal/alpha/excluded_test.go", "func": "TestGolden"`, 1))
	if err := os.WriteFile(receiptPath, receipt, 0o600); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, scope.Root, "internal/alpha/excluded_test.go", `//go:build cohesion_check_never

package alpha

import "testing"

func TestGolden(t *testing.T) {}
`)
	var out strings.Builder
	if err := CheckCohesion(cfg, scope, &out); err == nil {
		t.Fatalf("build-excluded golden path accepted:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "excluded by current build constraints") {
		t.Fatalf("unexpected report:\n%s", out.String())
	}
}

func TestGoTestExistsRejectsNonRunnableAnchorKinds(t *testing.T) {
	tests := []struct {
		name string
		fn   string
		want string
	}{
		{
			name: "benchmark",
			fn:   `func BenchmarkGolden(b *testing.B) {}`,
			want: "is not a runnable Go test, fuzz target, or example name",
		},
		{
			name: "lowercase example suffix",
			fn: `func Examplegolden() {
	// Output:
}`,
			want: "is not a runnable Go test, fuzz target, or example name",
		},
		{
			name: "example output is not final comment",
			fn: `func ExampleGolden() {
	// Output:
	// expected

	// A later comment prevents go/doc from recognizing Output.
}`,
			want: "has no recognized Output or Unordered output directive",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			funcName := strings.Fields(strings.TrimPrefix(tt.fn, "func "))[0]
			funcName = strings.SplitN(funcName, "(", 2)[0]
			mustWriteFile(t, root, "anchor_test.go", "package anchor\n\nimport \"testing\"\n\n"+tt.fn+"\n")
			_, err := goTestExists(root, CohesionTestRef{File: "anchor_test.go", Func: funcName}, build.Default)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("goTestExists() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCohesionExportInventoryIncludesOpaqueReceiverAndInterfaceMethods(t *testing.T) {
	cfg, scope := writeCohesionRepo(t, validCohesionReceipt)
	path := filepath.Join(scope.Root, "internal/alpha/alpha.go")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString(`

type opaque struct{}

func NewOpaque() opaque { return opaque{} }

func (opaque) HiddenReceiverOperation() {}

type Boundary interface {
	InterfaceOperation() error
}

func hiddenOperation() {}

var AliasOperation = hiddenOperation

var LiteralOperation = func() {}

var TypedOperation func()
`)
	closeErr := file.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	var out strings.Builder
	if err := CheckCohesion(cfg, scope, &out); err != nil {
		t.Fatalf("new exports are advisory by default: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"opaque.HiddenReceiverOperation",
		"Boundary.InterfaceOperation",
		"AliasOperation",
		"LiteralOperation",
		"TypedOperation",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("export inventory omitted %s:\n%s", want, out.String())
		}
	}
}

func TestCheckCohesionRejectsBuildExcludedProductionSymbol(t *testing.T) {
	receipt := strings.Replace(validCohesionReceipt,
		`"name": "Compose"`, `"name": "ExcludedCompose"`, 1)
	cfg, scope := writeCohesionRepo(t, receipt)
	mustWriteFile(t, scope.Root, "internal/alpha/excluded.go", `//go:build cohesion_check_never

package alpha

func ExcludedCompose() {}
`)
	var out strings.Builder
	if err := CheckCohesion(cfg, scope, &out); err == nil {
		t.Fatalf("build-excluded entrypoint accepted:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "symbol ExcludedCompose does not exist") {
		t.Fatalf("unexpected report:\n%s", out.String())
	}
}

func TestCheckCohesionRejectsCgoDisabledProductionSymbol(t *testing.T) {
	receipt := strings.Replace(validCohesionReceipt,
		`"name": "Compose"`, `"name": "CgoCompose"`, 1)
	cfg, scope := writeCohesionRepo(t, receipt)
	mustWriteFile(t, scope.Root, "internal/alpha/cgo.go", `package alpha

import "C"

func CgoCompose() {}
`)
	var out strings.Builder
	if err := CheckCohesion(cfg, scope, &out); err == nil {
		t.Fatalf("cgo-disabled entrypoint accepted:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "symbol CgoCompose does not exist") {
		t.Fatalf("unexpected report:\n%s", out.String())
	}
}

func TestCheckCohesionRejectsCgoGoldenPathTest(t *testing.T) {
	cfg, scope := writeCohesionRepo(t, validCohesionReceipt)
	receiptPath := filepath.Join(scope.Root, "cohesion.json")
	receipt, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	receipt = []byte(strings.Replace(string(receipt),
		`"file": "internal/alpha/alpha_test.go", "func": "TestGolden"`,
		`"file": "internal/alpha/cgo_test.go", "func": "TestGolden"`, 1))
	if err := os.WriteFile(receiptPath, receipt, 0o600); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, scope.Root, "internal/alpha/cgo_test.go", `package alpha

import "C"
import "testing"

func TestGolden(t *testing.T) {}
`)
	var out strings.Builder
	if err := CheckCohesion(cfg, scope, &out); err == nil {
		t.Fatalf("cgo test anchor accepted:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "cgo is not supported in Go test files") {
		t.Fatalf("unexpected report:\n%s", out.String())
	}
}

func TestCheckCohesionRejectsPackageSymlinkEscape(t *testing.T) {
	receipt := strings.Replace(validCohesionReceipt, `"packages": ["internal/alpha"]`, `"packages": ["linked"]`, 1)
	receipt = strings.ReplaceAll(receipt, `"package": "internal/alpha"`, `"package": "linked"`)
	cfg, scope := writeCohesionRepo(t, receipt)
	outside := t.TempDir()
	mustWriteFile(t, outside, "alpha.go", cohesionProduction)
	if err := os.Symlink(outside, filepath.Join(scope.Root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	var out strings.Builder
	if err := CheckCohesion(cfg, scope, &out); err == nil {
		t.Fatalf("out-of-root package symlink accepted:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "linked resolves outside repository root") {
		t.Fatalf("unexpected report:\n%s", out.String())
	}
}

func TestCheckCohesionRejectsGoldenTestSymlinkEscape(t *testing.T) {
	cfg, scope := writeCohesionRepo(t, validCohesionReceipt)
	receiptPath := filepath.Join(scope.Root, "cohesion.json")
	receipt, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	receipt = []byte(strings.Replace(string(receipt),
		`"file": "internal/alpha/alpha_test.go", "func": "TestGolden"`,
		`"file": "internal/alpha/external_test.go", "func": "TestGolden"`, 1))
	if err := os.WriteFile(receiptPath, receipt, 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "external_test.go")
	if err := os.WriteFile(outside, []byte("package alpha\n\nimport \"testing\"\n\nfunc TestGolden(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(scope.Root, "internal/alpha/external_test.go")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	var out strings.Builder
	if err := CheckCohesion(cfg, scope, &out); err == nil {
		t.Fatalf("out-of-root golden test symlink accepted:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "external_test.go resolves outside repository root") {
		t.Fatalf("unexpected report:\n%s", out.String())
	}
}

func TestLoadCohesionReceiptRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	tests := []struct {
		name    string
		receipt string
		want    string
	}{
		{"unknown field", `{"schemaVersion":1,"components":[],"extra":true}`, "unknown field"},
		{"trailing value", `{"schemaVersion":1,"components":[]} {}`, "trailing JSON value"},
		{"schema version", `{"schemaVersion":2,"components":[]}`, "unsupported schemaVersion 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cohesion.json")
			if err := os.WriteFile(path, []byte(tt.receipt), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadCohesionReceipt(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadCohesionReceipt() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCheckCohesionRequiresNamedCheckpointComponent(t *testing.T) {
	cfg, scope := writeCohesionRepo(t, validCohesionReceipt)
	scope.RequireComponents = []string{"beta"}
	var out strings.Builder
	if err := CheckCohesion(cfg, scope, &out); err == nil {
		t.Fatalf("missing required component accepted:\n%s", out.String())
	}
	if !strings.Contains(out.String(), `required component "beta" has no receipt`) {
		t.Fatalf("unexpected report:\n%s", out.String())
	}
}
