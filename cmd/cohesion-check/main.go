// Command cohesion-check validates reviewed component-internal composition
// receipts against a live requirements catalog, tagged-test evidence,
// architecture registry, and Go export surface.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	tracecheck "github.com/fkautz/trace-check"
)

// These values may be injected by a pinned module build, for example with
// -ldflags "-X main.buildRevision=<full-sha> -X main.buildModified=false".
var (
	buildVersion  string
	buildRevision string
	buildModified string
)

func main() {
	buildInfo, _ := debug.ReadBuildInfo()
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, buildInfo))
}

func run(args []string, stdout, stderr io.Writer, buildInfo *debug.BuildInfo) int {
	fs := flag.NewFlagSet("cohesion-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { usage(fs.Output()) }

	configPath := fs.String("config", "", "path to trace-check JSON dialect config; empty uses built-in defaults")
	root := fs.String("root", ".", "repository root")
	catalog := fs.String("catalog", "spec/requirements.md", "requirements catalog path, relative to -root")
	receipt := fs.String("receipt", "cohesion.json", "cohesion receipt path, relative to -root")
	architecture := fs.String("architecture", "", "architecture registry path, relative to -root; overrides config architecture.path")
	goos := fs.String("goos", "", "override receipt Go target OS for this audit")
	goarch := fs.String("goarch", "", "override receipt Go target architecture for this audit")
	buildTags := fs.String("tags", "", "override receipt Go build tags with a comma-separated list")
	warningsAsErrors := fs.Bool("warnings-as-errors", false, "fail advisory findings as well as receipt errors")
	format := fs.String("format", "text", "report format: text or json")
	showVersion := fs.Bool("version", false, "print module/build version and available VCS provenance")
	var requiredComponents stringListFlag
	fs.Var(&requiredComponents, "require-component", "require a named architecture component receipt; repeat or use comma-separated names")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, formatBuildVersion(buildInfo))
		return 0
	}
	if fs.NArg() > 0 {
		if fs.Arg(0) == "help" && fs.NArg() == 1 {
			usage(stdout)
			return 0
		}
		fmt.Fprintf(stderr, "cohesion-check: unexpected argument %q (this tool takes flags only; run `cohesion-check -help`)\n", fs.Arg(0))
		return 2
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "cohesion-check: invalid -format %q (want text or json)\n", *format)
		return 2
	}

	cfg := tracecheck.Default()
	if *configPath != "" {
		loaded, err := tracecheck.LoadConfig(*configPath)
		if err != nil {
			fmt.Fprintf(stderr, "cohesion-check: %v\n", err)
			return 2
		}
		cfg = loaded
	} else if err := cfg.Validate(); err != nil {
		fmt.Fprintf(stderr, "cohesion-check: %v\n", err)
		return 2
	}

	scope := tracecheck.CohesionScope{
		Root:              *root,
		Catalog:           *catalog,
		Receipt:           *receipt,
		ArchitecturePath:  *architecture,
		RequireComponents: []string(requiredComponents),
		WarningsAsErrors:  *warningsAsErrors,
		GOOS:              *goos,
		GOARCH:            *goarch,
	}
	tagsFlagSet := false
	fs.Visit(func(visited *flag.Flag) {
		if visited.Name == "tags" {
			tagsFlagSet = true
		}
	})
	if tagsFlagSet {
		// InspectCohesion distinguishes nil (use receipt tags) from an explicit
		// override. Preserve a non-nil empty slice so -tags= clears receipt tags.
		scope.BuildTags = append([]string{}, splitList(*buildTags)...)
	}
	report, err := tracecheck.InspectCohesion(&cfg, scope)
	if err != nil {
		fmt.Fprintf(stderr, "cohesion-check: %v\n", err)
		var scopeErr *tracecheck.ScopeError
		if errors.As(err, &scopeErr) {
			return 2
		}
		return 1
	}
	if *format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "cohesion-check: write JSON report: %v\n", err)
			return 1
		}
	} else if err := report.WriteText(stdout); err != nil {
		fmt.Fprintf(stderr, "cohesion-check: write report: %v\n", err)
		return 1
	}
	if report.HasErrors() || (*warningsAsErrors && report.HasWarnings()) {
		return 1
	}
	return 0
}

func formatBuildVersion(info *debug.BuildInfo) string {
	version := "unknown"
	revision := "unknown"
	modified := "unknown"
	if info != nil {
		if info.Main.Version != "" {
			version = info.Main.Version
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value
			}
		}
	}
	if buildVersion != "" {
		version = buildVersion
	}
	if buildRevision != "" {
		revision = buildRevision
	}
	if buildModified != "" {
		modified = buildModified
	}
	return fmt.Sprintf("cohesion-check version=%s revision=%s modified=%s", version, revision, modified)
}

type stringListFlag []string

func (f *stringListFlag) String() string { return strings.Join(*f, ",") }

func (f *stringListFlag) Set(value string) error {
	for _, item := range splitList(value) {
		*f = append(*f, item)
	}
	return nil
}

func splitList(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func usage(w io.Writer) {
	fmt.Fprint(w, usageText)
}

const usageText = `cohesion-check — validate component-internal composition receipts

USAGE
  cohesion-check -config tracecheck.json [flags]

PURPOSE
  cohesion-check is a read-only companion to trace-check. trace-check answers
  which normative requirements have valid tagged-test or waiver evidence.
  cohesion-check answers whether a reviewed component-cohesion claim still
  points at real packages, callables, tagged tests, architecture names, and a
  declared golden path. It never creates or satisfies requirement coverage.

FLAGS
  -root DIR                 repository root (default .)
  -config FILE              trace-check dialect config; empty uses defaults
  -catalog FILE             requirements catalog (default spec/requirements.md)
  -receipt FILE             cohesion receipt (default cohesion.json)
  -architecture FILE        architecture registry override; otherwise use config
  -goos GOOS                override receipt Go target OS for this audit
  -goarch GOARCH            override receipt Go target architecture for this audit
  -tags TAGS                override receipt Go build tags; empty clears them
  -require-component NAME   require NAME in this checkpoint; repeat or comma-separate
  -warnings-as-errors       promote advisories to a failing exit status
  -format text|json         deterministic report format (default text)
  -version                  print build provenance

RECEIPT SCHEMA (version 1, abbreviated)
  {
    "schemaVersion": 1,
    "goBuild": {
      "goos": "linux", "goarch": "amd64", "goVersion": "go1.25",
      "compiler": "gc", "cgoEnabled": false, "tags": [], "toolTags": []
    },
    "components": [{
      "component": "materializer",
      "status": "cohesive | compose-needed | blocked",
      "rationale": "required for compose-needed or blocked",
      "packages": ["internal/materializer"],
      "islands": [{
        "name": "verify-oci",
        "symbols": [{"package":"internal/materializer","name":"VerifyOCI"}],
        "evidence": [{
          "requirement": "MAT-3",
          "tests": [{"file":"conformance/oci_test.go","func":"TestOCI"}]
        }],
        "invariants": ["I-VERIFY"]
      }],
      "operations": [{
        "name": "materialize-base",
        "entrypoint": {"package":"internal/materializer","name":"MaterializeBase"},
        "publishPoint": "successful return after LLT1 and LLAY1 admission",
        "stages": ["verify-oci", "emit-layout"],
        "goldenPathTest": {"file":"integration/base_test.go","func":"TestMaterializeBase"},
        "invariants": ["I-VERIFY"],
        "delegates": [],
        "retired": []
      }],
      "primitives": [{
        "symbol": {"package":"internal/materializer","name":"ParsePath"},
        "rationale": "stable low-level primitive, not a product operation"
      }]
    }]
  }

  A certified island must name exact live tagged-test evidence. A waiver, a
  matrix row whose covered bit comes only from a waiver, or an unrelated tag
  cannot certify an island. Catalog Component metadata remains routing: cited
  requirements need not share the receipt component when code placement and
  routing legitimately differ.

  Within one component receipt, one exact requirement/test evidence link
  certifies at most one island. If two stage labels would reuse it, group their
  symbols into one island or cite independently tagged tests that actually
  distinguish the behaviors. Cross-component reuse is allowed when one test
  genuinely exercises both sides of a seam; catalog Component remains routing,
  not inferred code ownership.

  goBuild makes source selection reproducible instead of inheriting the host
  GOOS/GOARCH, Go release tags, compiler tags, cgo, or tool-experiment tags.
  The target registry is pinned to Go 1.25 and the report records the effective
  context. -goos, -goarch, and -tags are deliberate cross-target audit
  overrides; normal gates use the receipt-pinned context.

  cohesive requires at least one operation and every listed island to appear
  in an ordered operation stage list. An operation has at least two distinct
  stages, one exported entrypoint, one existing golden-path test, and a named
  publish point. Delegates must exist; retired callables must not. Exported
  callables that are not an entrypoint, delegate, or rationalized primitive
  are advisory findings. Exported island functions are not automatically
  blessed as permanent primitives.

ADVISORIES
  compose-needed or blocked status, unclassified exported callables, an
  incomplete island inventory, and a requirement-tagged golden-path test are
  advisory by default. Use -warnings-as-errors only after reviewing and
  ratcheting the component's receipt. -require-component makes a named
  checkpoint fail if the receipt omits that component.

NON-GOALS
  Go AST presence cannot prove runtime call order, thin delegation, absence of
  every bypass, fail-closed behavior, a single publication point, or that a
  named test actually invokes a named symbol. Those remain TDD/mutation
  evidence plus independent review. The tool validates the receipt's
  machine-checkable anchors and makes export-surface drift loud. Its advisory
  export inventory covers declared functions/methods, direct interface
  methods, and AST-inferable function variables; it is not a type-checked call
  graph or exhaustive API-compatibility analyzer.

EXIT STATUS
  0  receipt anchors valid; advisories allowed unless promoted
  1  semantic findings, promoted advisories, or runtime/I/O failure
  2  bad flags, config, scope, or receipt schema

EXAMPLES
  cohesion-check -config tracecheck.json
  cohesion-check -config tracecheck.json -require-component materializer
  cohesion-check -config tracecheck.json -format json
  cohesion-check -config tracecheck.json -warnings-as-errors
`
