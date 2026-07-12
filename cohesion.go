package tracecheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// CohesionSchemaVersion is the receipt schema understood by cohesion-check.
const CohesionSchemaVersion = 1

const (
	// CohesionError marks a receipt finding that invalidates a machine-checkable claim.
	CohesionError = "error"
	// CohesionWarning marks an advisory that may be ratcheted with WarningsAsErrors.
	CohesionWarning = "warning"
)

// CohesionReceipt records reviewed component-internal composition claims.
// It is evidence provenance, not requirement coverage.
type CohesionReceipt struct {
	SchemaVersion int                        `json:"schemaVersion"`
	GoBuild       CohesionGoBuild            `json:"goBuild"`
	Components    []CohesionComponentReceipt `json:"components"`
}

// CohesionGoBuild pins the Go source-selection context used for AST anchors.
type CohesionGoBuild struct {
	GOOS       string   `json:"goos"`
	GOARCH     string   `json:"goarch"`
	GoVersion  string   `json:"goVersion"`
	Compiler   string   `json:"compiler"`
	CgoEnabled bool     `json:"cgoEnabled"`
	Tags       []string `json:"tags"`
	ToolTags   []string `json:"toolTags"`
}

// CohesionComponentReceipt is one architecture component's current cohesion audit.
type CohesionComponentReceipt struct {
	Component  string                     `json:"component"`
	Status     string                     `json:"status"`
	Rationale  string                     `json:"rationale,omitempty"`
	Packages   []string                   `json:"packages"`
	Islands    []CohesionIslandReceipt    `json:"islands,omitempty"`
	Operations []CohesionOperationReceipt `json:"operations,omitempty"`
	Primitives []CohesionPrimitiveReceipt `json:"primitives,omitempty"`
}

// CohesionSymbolRef identifies a Go function or Type.Method in a root-relative package.
type CohesionSymbolRef struct {
	Package string `json:"package"`
	Name    string `json:"name"`
}

// CohesionTestRef identifies a runnable Go test, fuzz target, or example.
type CohesionTestRef struct {
	File string `json:"file"`
	Func string `json:"func"`
}

// CohesionEvidenceRef binds a catalog requirement to exact tagged tests.
type CohesionEvidenceRef struct {
	Requirement string            `json:"requirement"`
	Tests       []CohesionTestRef `json:"tests"`
}

// CohesionIslandReceipt describes one tested production island inside a component.
type CohesionIslandReceipt struct {
	Name       string                `json:"name"`
	Symbols    []CohesionSymbolRef   `json:"symbols"`
	Evidence   []CohesionEvidenceRef `json:"evidence"`
	Invariants []string              `json:"invariants,omitempty"`
}

// CohesionOperationReceipt describes one ordered authoritative golden path.
type CohesionOperationReceipt struct {
	Name           string              `json:"name"`
	Entrypoint     CohesionSymbolRef   `json:"entrypoint"`
	PublishPoint   string              `json:"publishPoint"`
	Stages         []string            `json:"stages"`
	GoldenPathTest CohesionTestRef     `json:"goldenPathTest"`
	Invariants     []string            `json:"invariants,omitempty"`
	Delegates      []CohesionSymbolRef `json:"delegates,omitempty"`
	Retired        []CohesionSymbolRef `json:"retired,omitempty"`
}

// CohesionPrimitiveReceipt rationalizes one intentionally public non-operation callable.
type CohesionPrimitiveReceipt struct {
	Symbol    CohesionSymbolRef `json:"symbol"`
	Rationale string            `json:"rationale"`
}

// CohesionScope selects the repository inputs and checkpoint policy.
type CohesionScope struct {
	Root              string
	Catalog           string
	Receipt           string
	ArchitecturePath  string
	RequireComponents []string
	WarningsAsErrors  bool
	GOOS              string
	GOARCH            string
	BuildTags         []string
}

// CohesionFinding is one deterministic receipt error or advisory.
type CohesionFinding struct {
	Severity  string `json:"severity"`
	Code      string `json:"code"`
	Component string `json:"component,omitempty"`
	Operation string `json:"operation,omitempty"`
	Message   string `json:"message"`
}

// CohesionComponentSummary is the compact per-component report header.
type CohesionComponentSummary struct {
	Component  string `json:"component"`
	Status     string `json:"status"`
	Islands    int    `json:"islands"`
	Operations int    `json:"operations"`
}

// CohesionReport is the machine-readable result of one inspection.
type CohesionReport struct {
	SchemaVersion  int                        `json:"schemaVersion"`
	GoBuild        CohesionGoBuild            `json:"goBuild"`
	Components     []CohesionComponentSummary `json:"components"`
	IslandCount    int                        `json:"islandCount"`
	OperationCount int                        `json:"operationCount"`
	Findings       []CohesionFinding          `json:"findings"`
}

// CohesionFindingsError means inspection completed and reported findings that
// the selected policy treats as failing.
type CohesionFindingsError struct {
	Errors   int
	Warnings int
}

func (e *CohesionFindingsError) Error() string {
	if e.Errors > 0 {
		return fmt.Sprintf("cohesion receipt has %d error(s) and %d advisory finding(s)", e.Errors, e.Warnings)
	}
	return fmt.Sprintf("cohesion receipt has %d advisory finding(s) and warnings-as-errors is enabled", e.Warnings)
}

// LoadCohesionReceipt strictly decodes a versioned cohesion receipt.
func LoadCohesionReceipt(path string) (CohesionReceipt, error) {
	f, err := os.Open(path) // #nosec G304 -- operator-supplied read-only input
	if err != nil {
		return CohesionReceipt{}, err
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var receipt CohesionReceipt
	if err := dec.Decode(&receipt); err != nil {
		return CohesionReceipt{}, fmt.Errorf("decode cohesion receipt %s: %w", path, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return CohesionReceipt{}, fmt.Errorf("decode cohesion receipt %s: trailing JSON value", path)
		}
		return CohesionReceipt{}, fmt.Errorf("decode cohesion receipt %s: trailing JSON value: %w", path, err)
	}
	if receipt.SchemaVersion != CohesionSchemaVersion {
		return CohesionReceipt{}, fmt.Errorf("unsupported schemaVersion %d (want %d)", receipt.SchemaVersion, CohesionSchemaVersion)
	}
	return receipt, nil
}

// CheckCohesion inspects a receipt, writes a deterministic text report, and
// fails for semantic errors (or advisories when WarningsAsErrors is selected).
func CheckCohesion(cfg *Config, scope CohesionScope, w io.Writer) error {
	report, err := InspectCohesion(cfg, scope)
	if err != nil {
		return err
	}
	if err := report.WriteText(w); err != nil {
		return err
	}
	errorsCount, warningsCount := report.counts()
	if errorsCount > 0 || (scope.WarningsAsErrors && warningsCount > 0) {
		return &CohesionFindingsError{Errors: errorsCount, Warnings: warningsCount}
	}
	return nil
}

// InspectCohesion validates receipt anchors against the live catalog,
// architecture registry, coverage tags, and Go export surface.
func InspectCohesion(cfg *Config, scope CohesionScope) (CohesionReport, error) {
	report := CohesionReport{
		SchemaVersion: CohesionSchemaVersion,
		Components:    make([]CohesionComponentSummary, 0),
		Findings:      make([]CohesionFinding, 0),
	}
	if cfg == nil {
		return report, &ScopeError{Err: errors.New("nil config")}
	}
	root := scope.Root
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return report, &ScopeError{Err: fmt.Errorf("resolve root: %w", err)}
	}
	rootAbs = filepath.Clean(rootAbs)
	if strings.TrimSpace(scope.Catalog) == "" {
		return report, &ScopeError{Err: errors.New("catalog path is required")}
	}
	if strings.TrimSpace(scope.Receipt) == "" {
		return report, &ScopeError{Err: errors.New("receipt path is required")}
	}
	catalogPath := resolveCohesionInput(rootAbs, scope.Catalog)
	receiptPath := resolveCohesionInput(rootAbs, scope.Receipt)
	archPath := scope.ArchitecturePath
	if strings.TrimSpace(archPath) == "" {
		archPath = cfg.Architecture.Path
	}
	if strings.TrimSpace(archPath) == "" {
		return report, &ScopeError{Err: errors.New("architecture registry path is required")}
	}
	archPath = resolveCohesionInput(rootAbs, archPath)

	receipt, err := LoadCohesionReceipt(receiptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return report, err
		}
		return report, &ScopeError{Err: err}
	}
	report.SchemaVersion = receipt.SchemaVersion
	buildContext, effectiveBuild, err := effectiveCohesionBuild(receipt.GoBuild, scope)
	if err != nil {
		return report, &ScopeError{Err: err}
	}
	report.GoBuild = effectiveBuild

	reqs, catalogProblems, err := ParseCatalog(cfg, catalogPath)
	if err != nil {
		return report, err
	}
	arch, archProblems, err := LoadArchitecture(cfg, archPath)
	if err != nil {
		return report, err
	}
	if arch == nil {
		return report, &ScopeError{Err: errors.New("architecture registry did not load")}
	}
	tags, tagProblems, err := CollectTags(cfg, rootAbs)
	if err != nil {
		return report, err
	}
	for _, problem := range catalogProblems {
		report.add(CohesionError, "catalog-integrity", "", "", problem)
	}
	for _, problem := range archProblems {
		report.add(CohesionError, "architecture-integrity", "", "", problem)
	}
	for _, problem := range cfg.validateCatalogMeta(reqs, arch) {
		report.add(CohesionError, "catalog-integrity", "", "", problem)
	}
	for _, problem := range tagProblems {
		report.add(CohesionError, "tag-integrity", "", "", problem)
	}

	requirements := make(map[string]Requirement, len(reqs))
	for _, req := range reqs {
		requirements[req.ID] = req
		if req.Class == "" {
			report.add(CohesionError, "catalog-integrity", "", "", fmt.Sprintf("%s: missing or unclassifiable Keyword line in catalog", req.ID))
		}
	}
	taggedTests := make(map[string][]string)
	for id, refs := range tags {
		for _, ref := range refs {
			key := cohesionTestKey(ref.File, ref.Func)
			taggedTests[key] = append(taggedTests[key], id)
		}
	}
	for key := range taggedTests {
		sort.Strings(taggedTests[key])
	}

	if len(receipt.Components) == 0 {
		report.add(CohesionError, "empty-receipt", "", "", "receipt has no components")
	}
	componentSeen := map[string]bool{}
	inventories := map[string]goPackageInventory{}
	inventoryErrors := map[string]error{}
	for _, component := range receipt.Components {
		name := strings.TrimSpace(component.Component)
		report.Components = append(report.Components, CohesionComponentSummary{
			Component: name, Status: component.Status,
			Islands: len(component.Islands), Operations: len(component.Operations),
		})
		report.IslandCount += len(component.Islands)
		report.OperationCount += len(component.Operations)
		if name == "" {
			report.add(CohesionError, "component-name", name, "", "component name is empty")
		} else if componentSeen[name] {
			report.add(CohesionError, "duplicate-component", name, "", fmt.Sprintf("component %q appears more than once", name))
		} else {
			componentSeen[name] = true
		}
		if name != "" && !arch.HasComponent(name) {
			report.add(CohesionError, "unknown-component", name, "", fmt.Sprintf("component %q is not registered in the architecture", name))
		}
		inspectCohesionComponent(rootAbs, buildContext, arch, requirements, tags, taggedTests, component, &report, inventories, inventoryErrors)
	}

	for _, required := range scope.RequireComponents {
		required = strings.TrimSpace(required)
		if required == "" {
			report.add(CohesionError, "required-component", "", "", "required component name is empty")
			continue
		}
		if !arch.HasComponent(required) {
			report.add(CohesionError, "required-component", required, "", fmt.Sprintf("required component %q is not registered in the architecture", required))
		}
		if !componentSeen[required] {
			report.add(CohesionError, "required-component", required, "", fmt.Sprintf("required component %q has no receipt", required))
		}
	}

	sort.Slice(report.Components, func(i, j int) bool {
		return report.Components[i].Component < report.Components[j].Component
	})
	report.sortFindings()
	return report, nil
}

func inspectCohesionComponent(
	root string,
	buildContext build.Context,
	arch *Architecture,
	requirements map[string]Requirement,
	tags map[string][]TagRef,
	taggedTests map[string][]string,
	component CohesionComponentReceipt,
	report *CohesionReport,
	inventories map[string]goPackageInventory,
	inventoryErrors map[string]error,
) {
	name := strings.TrimSpace(component.Component)
	status := strings.TrimSpace(component.Status)
	switch status {
	case "cohesive":
		if len(component.Operations) == 0 {
			report.add(CohesionError, "cohesive-without-operation", name, "", "cohesive component has no authoritative operation")
		}
	case "compose-needed", "blocked":
		if strings.TrimSpace(component.Rationale) == "" {
			report.add(CohesionError, "missing-rationale", name, "", fmt.Sprintf("status %s requires a rationale", status))
		}
		report.add(CohesionWarning, "component-status", name, "", fmt.Sprintf("status is %s: %s", status, strings.TrimSpace(component.Rationale)))
	default:
		report.add(CohesionError, "invalid-status", name, "", fmt.Sprintf("invalid status %q (want cohesive, compose-needed, or blocked)", status))
	}
	if status != "cohesive" && len(component.Islands) < 2 {
		report.add(CohesionWarning, "island-inventory", name, "", fmt.Sprintf("status %s inventories fewer than two islands", status))
	}
	if len(component.Packages) == 0 {
		report.add(CohesionError, "missing-packages", name, "", "component has no Go package directories")
	}

	packageSet := map[string]bool{}
	for _, pkg := range component.Packages {
		pkg = filepath.ToSlash(strings.TrimSpace(pkg))
		if err := validateCohesionRelativePath(pkg); err != nil {
			report.add(CohesionError, "package-path", name, "", fmt.Sprintf("package %q: %v", pkg, err))
			continue
		}
		if packageSet[pkg] {
			report.add(CohesionError, "duplicate-package", name, "", fmt.Sprintf("package %q appears more than once", pkg))
			continue
		}
		packageSet[pkg] = true
		if _, scanned := inventories[pkg]; !scanned && inventoryErrors[pkg] == nil {
			inventory, err := scanGoPackage(root, pkg, buildContext)
			if err != nil {
				inventoryErrors[pkg] = err
			} else {
				inventories[pkg] = inventory
			}
		}
		if err := inventoryErrors[pkg]; err != nil {
			report.add(CohesionError, "package-scan", name, "", fmt.Sprintf("package %s: %v", pkg, err))
		}
	}

	validateSymbol := func(ref CohesionSymbolRef, exported bool, context, operation string) bool {
		pkg := filepath.ToSlash(strings.TrimSpace(ref.Package))
		symbol := strings.TrimSpace(ref.Name)
		if pkg == "" || symbol == "" {
			report.add(CohesionError, "symbol-ref", name, operation, fmt.Sprintf("%s has an empty package or symbol name", context))
			return false
		}
		if !validCohesionSymbolName(symbol) {
			report.add(CohesionError, "symbol-ref", name, operation, fmt.Sprintf("%s symbol name %q is not Func or Type.Method form", context, symbol))
			return false
		}
		if !packageSet[pkg] {
			report.add(CohesionError, "symbol-package", name, operation, fmt.Sprintf("%s symbol %s uses undeclared package %s", context, symbol, pkg))
			return false
		}
		inventory, ok := inventories[pkg]
		if !ok {
			return false
		}
		info, ok := inventory.Symbols[symbol]
		if !ok {
			report.add(CohesionError, "missing-symbol", name, operation, fmt.Sprintf("%s symbol %s does not exist in %s", context, symbol, pkg))
			return false
		}
		if exported && !info.Exported {
			report.add(CohesionError, "unexported-symbol", name, operation, fmt.Sprintf("%s symbol %s is not an exported callable", context, symbol))
			return false
		}
		return true
	}

	islandNames := map[string]bool{}
	evidenceOwners := map[string]string{}
	for _, island := range component.Islands {
		islandName := strings.TrimSpace(island.Name)
		if islandName == "" {
			report.add(CohesionError, "island-name", name, "", "island name is empty")
		} else if islandNames[islandName] {
			report.add(CohesionError, "duplicate-island", name, "", fmt.Sprintf("island %q appears more than once", islandName))
		} else {
			islandNames[islandName] = true
		}
		if len(island.Symbols) == 0 {
			report.add(CohesionError, "island-symbol", name, "", fmt.Sprintf("island %q has no production symbols", islandName))
		}
		seenSymbols := map[string]bool{}
		for _, ref := range island.Symbols {
			key := cohesionSymbolKey(ref)
			if seenSymbols[key] {
				report.add(CohesionError, "duplicate-symbol", name, "", fmt.Sprintf("island %q repeats symbol %s", islandName, ref.Name))
				continue
			}
			seenSymbols[key] = true
			validateSymbol(ref, false, fmt.Sprintf("island %q", islandName), "")
		}
		if len(island.Evidence) == 0 {
			report.add(CohesionError, "island-evidence", name, "", fmt.Sprintf("island %q has no tagged-test evidence", islandName))
		}
		seenRequirements := map[string]bool{}
		for _, evidence := range island.Evidence {
			id := strings.TrimSpace(evidence.Requirement)
			if seenRequirements[id] {
				report.add(CohesionError, "duplicate-evidence", name, "", fmt.Sprintf("island %q repeats requirement %s", islandName, id))
				continue
			}
			seenRequirements[id] = true
			if _, ok := requirements[id]; !ok {
				report.add(CohesionError, "unknown-requirement", name, "", fmt.Sprintf("island %q cites unknown requirement %s", islandName, id))
			}
			if len(tags[id]) == 0 {
				report.add(CohesionError, "untagged-requirement", name, "", fmt.Sprintf("island %q requirement %s has no tagged test evidence", islandName, id))
			}
			if len(evidence.Tests) == 0 {
				report.add(CohesionError, "missing-evidence-test", name, "", fmt.Sprintf("island %q requirement %s names no evidence tests", islandName, id))
			}
			seenTests := map[string]bool{}
			for _, test := range evidence.Tests {
				key, err := validateCohesionTestRef(test)
				if err != nil {
					report.add(CohesionError, "evidence-test", name, "", fmt.Sprintf("island %q requirement %s: %v", islandName, id, err))
					continue
				}
				if seenTests[key] {
					report.add(CohesionError, "duplicate-evidence-test", name, "", fmt.Sprintf("island %q requirement %s repeats test %s", islandName, id, key))
					continue
				}
				seenTests[key] = true
				evidenceKey := id + "\x00" + key
				if previous, exists := evidenceOwners[evidenceKey]; exists && previous != islandName {
					report.add(CohesionError, "shared-island-evidence", name, "", fmt.Sprintf("tagged evidence %s at %s is reused by islands %q and %q; group the symbols into one certified island or cite distinct evidence", id, key, previous, islandName))
				} else {
					evidenceOwners[evidenceKey] = islandName
				}
				if !hasTagRef(tags[id], test) {
					report.add(CohesionError, "evidence-test", name, "", fmt.Sprintf("island %q evidence test %s is not tagged for %s", islandName, test.Func, id))
					continue
				}
				exists, testErr := goTestExists(root, test, buildContext)
				if testErr != nil {
					report.add(CohesionError, "evidence-test", name, "", fmt.Sprintf("island %q evidence test %s is not runnable: %v", islandName, test.Func, testErr))
				} else if !exists {
					report.add(CohesionError, "evidence-test", name, "", fmt.Sprintf("island %q evidence test %s does not exist", islandName, test.Func))
				}
			}
		}
		for _, invariant := range island.Invariants {
			validateCohesionInvariant(arch, report, name, "", islandName, invariant)
		}
	}

	classifiedExports := map[string]string{}
	registerRole := func(ref CohesionSymbolRef, role string) {
		key := cohesionSymbolKey(ref)
		if previous, exists := classifiedExports[key]; exists {
			report.add(CohesionError, "symbol-role", name, "", fmt.Sprintf("symbol %s is both %s and %s", ref.Name, previous, role))
			return
		}
		classifiedExports[key] = role
	}
	usedIslands := map[string]bool{}
	operationNames := map[string]bool{}
	for _, operation := range component.Operations {
		opName := strings.TrimSpace(operation.Name)
		if opName == "" {
			report.add(CohesionError, "operation-name", name, opName, "operation name is empty")
		} else if operationNames[opName] {
			report.add(CohesionError, "duplicate-operation", name, opName, fmt.Sprintf("operation %q appears more than once", opName))
		} else {
			operationNames[opName] = true
		}
		if validateSymbol(operation.Entrypoint, true, fmt.Sprintf("operation %q entrypoint", opName), opName) {
			registerRole(operation.Entrypoint, fmt.Sprintf("entrypoint for %s", opName))
		}
		if strings.TrimSpace(operation.PublishPoint) == "" {
			report.add(CohesionError, "publish-point", name, opName, "operation has no declared publish point")
		}
		stageSeen := map[string]bool{}
		for _, stage := range operation.Stages {
			stage = strings.TrimSpace(stage)
			if stageSeen[stage] {
				report.add(CohesionError, "duplicate-stage", name, opName, fmt.Sprintf("operation %q duplicates stage %q", opName, stage))
				continue
			}
			stageSeen[stage] = true
			if !islandNames[stage] {
				report.add(CohesionError, "unknown-stage", name, opName, fmt.Sprintf("operation %q references unknown stage %q", opName, stage))
				continue
			}
			usedIslands[stage] = true
		}
		if len(stageSeen) < 2 {
			report.add(CohesionError, "stage-count", name, opName, fmt.Sprintf("operation %q composes fewer than two distinct island stages", opName))
		}
		for _, invariant := range operation.Invariants {
			validateCohesionInvariant(arch, report, name, opName, opName, invariant)
		}
		if _, err := validateCohesionTestRef(operation.GoldenPathTest); err != nil {
			report.add(CohesionError, "golden-test", name, opName, fmt.Sprintf("operation %q: %v", opName, err))
		} else {
			exists, err := goTestExists(root, operation.GoldenPathTest, buildContext)
			if err != nil {
				report.add(CohesionError, "golden-test", name, opName, fmt.Sprintf("operation %q golden-path test: %v", opName, err))
			} else if !exists {
				report.add(CohesionError, "golden-test", name, opName, fmt.Sprintf("operation %q golden-path test %s does not exist in %s", opName, operation.GoldenPathTest.Func, operation.GoldenPathTest.File))
			}
			key := cohesionTestKey(operation.GoldenPathTest.File, operation.GoldenPathTest.Func)
			if ids := taggedTests[key]; len(ids) > 0 {
				report.add(CohesionWarning, "tagged-golden-test", name, opName, fmt.Sprintf("operation %q golden-path test is requirement-tagged (%s); confirm it is not synthetic or unrelated", opName, strings.Join(ids, ", ")))
			}
		}
		delegateSeen := map[string]bool{}
		for _, delegate := range operation.Delegates {
			key := cohesionSymbolKey(delegate)
			if delegateSeen[key] {
				report.add(CohesionError, "duplicate-delegate", name, opName, fmt.Sprintf("operation %q repeats delegate %s", opName, delegate.Name))
				continue
			}
			delegateSeen[key] = true
			if validateSymbol(delegate, true, fmt.Sprintf("operation %q delegate", opName), opName) {
				registerRole(delegate, fmt.Sprintf("delegate for %s", opName))
			}
		}
		retiredSeen := map[string]bool{}
		for _, retired := range operation.Retired {
			pkg := filepath.ToSlash(strings.TrimSpace(retired.Package))
			symbol := strings.TrimSpace(retired.Name)
			key := cohesionSymbolKey(retired)
			if retiredSeen[key] {
				report.add(CohesionError, "duplicate-retired", name, opName, fmt.Sprintf("operation %q repeats retired symbol %s", opName, retired.Name))
				continue
			}
			retiredSeen[key] = true
			if !packageSet[pkg] {
				report.add(CohesionError, "retired-package", name, opName, fmt.Sprintf("operation %q retired symbol %s uses undeclared package %s", opName, retired.Name, pkg))
				continue
			}
			if !validCohesionSymbolName(symbol) {
				report.add(CohesionError, "retired-symbol-ref", name, opName, fmt.Sprintf("operation %q retired symbol name %q is empty or invalid", opName, retired.Name))
				continue
			}
			if inventory, ok := inventories[pkg]; ok {
				if _, exists := inventory.Symbols[symbol]; exists {
					report.add(CohesionError, "retired-symbol", name, opName, fmt.Sprintf("operation %q retired symbol %s still exists in %s", opName, retired.Name, pkg))
				}
			}
		}
	}
	if status == "cohesive" {
		for island := range islandNames {
			if !usedIslands[island] {
				report.add(CohesionError, "unstaged-island", name, "", fmt.Sprintf("cohesive component leaves island %q outside every authoritative operation", island))
			}
		}
	}

	primitiveSeen := map[string]bool{}
	for _, primitive := range component.Primitives {
		key := cohesionSymbolKey(primitive.Symbol)
		if primitiveSeen[key] {
			report.add(CohesionError, "duplicate-primitive", name, "", fmt.Sprintf("primitive %s appears more than once", primitive.Symbol.Name))
			continue
		}
		primitiveSeen[key] = true
		if strings.TrimSpace(primitive.Rationale) == "" {
			report.add(CohesionError, "primitive-rationale", name, "", fmt.Sprintf("primitive %s has no rationale", primitive.Symbol.Name))
		}
		if validateSymbol(primitive.Symbol, true, "primitive", "") {
			registerRole(primitive.Symbol, "primitive")
		}
	}

	for pkg := range packageSet {
		inventory, ok := inventories[pkg]
		if !ok {
			continue
		}
		var unclassified []string
		for _, symbol := range inventory.Exported {
			key := pkg + ":" + symbol
			if _, classified := classifiedExports[key]; !classified {
				unclassified = append(unclassified, symbol)
			}
		}
		if len(unclassified) == 1 {
			report.add(CohesionWarning, "unclassified-export", name, "", fmt.Sprintf("package %s has unclassified exported callable %s", pkg, unclassified[0]))
		} else if len(unclassified) > 1 {
			report.add(CohesionWarning, "unclassified-export", name, "", fmt.Sprintf("package %s has unclassified exported callables: %s", pkg, strings.Join(unclassified, ", ")))
		}
	}
}

func validateCohesionInvariant(arch *Architecture, report *CohesionReport, component, operation, owner, invariant string) {
	invariant = strings.TrimSpace(invariant)
	if !arch.HasInvariant(invariant) {
		report.add(CohesionError, "unknown-invariant", component, operation, fmt.Sprintf("%s: invariant %q is not registered in the architecture", owner, invariant))
	}
}

func resolveCohesionInput(root, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, filepath.FromSlash(path))
}

func effectiveCohesionBuild(receipt CohesionGoBuild, scope CohesionScope) (build.Context, CohesionGoBuild, error) {
	effective := receipt
	if strings.TrimSpace(scope.GOOS) != "" {
		effective.GOOS = strings.TrimSpace(scope.GOOS)
	}
	if strings.TrimSpace(scope.GOARCH) != "" {
		effective.GOARCH = strings.TrimSpace(scope.GOARCH)
	}
	if scope.BuildTags != nil {
		effective.Tags = make([]string, len(scope.BuildTags))
		copy(effective.Tags, scope.BuildTags)
	} else {
		effective.Tags = make([]string, len(receipt.Tags))
		copy(effective.Tags, receipt.Tags)
	}
	if !supportedCohesionGoTargets[effective.GOOS+"/"+effective.GOARCH] {
		return build.Context{}, CohesionGoBuild{}, fmt.Errorf("goBuild target %q is not supported by the pinned Go 1.25 target registry", effective.GOOS+"/"+effective.GOARCH)
	}
	if effective.Compiler != "gc" {
		return build.Context{}, CohesionGoBuild{}, fmt.Errorf("goBuild.compiler %q is unsupported by schema version 1 (want gc)", effective.Compiler)
	}
	if effective.GoVersion != "go1.25" {
		return build.Context{}, CohesionGoBuild{}, fmt.Errorf("goBuild.goVersion %q is unsupported by schema version 1 (want go1.25)", effective.GoVersion)
	}
	releaseTags, err := cohesionReleaseTags(effective.GoVersion)
	if err != nil {
		return build.Context{}, CohesionGoBuild{}, err
	}
	effective.Tags, err = normalizeCohesionBuildTags("goBuild.tags", effective.Tags)
	if err != nil {
		return build.Context{}, CohesionGoBuild{}, err
	}
	effective.ToolTags, err = normalizeCohesionBuildTags("goBuild.toolTags", append([]string{}, receipt.ToolTags...))
	if err != nil {
		return build.Context{}, CohesionGoBuild{}, err
	}
	ctx := build.Default
	ctx.GOOS = effective.GOOS
	ctx.GOARCH = effective.GOARCH
	ctx.Compiler = effective.Compiler
	ctx.CgoEnabled = effective.CgoEnabled
	ctx.BuildTags = append([]string(nil), effective.Tags...)
	ctx.ToolTags = append([]string(nil), effective.ToolTags...)
	ctx.ReleaseTags = releaseTags
	return ctx, effective, nil
}

func normalizeCohesionBuildTags(label string, input []string) ([]string, error) {
	out := make([]string, len(input))
	seen := map[string]bool{}
	for i, raw := range input {
		tag := strings.TrimSpace(raw)
		if !validCohesionBuildTag(tag) {
			return nil, fmt.Errorf("%s[%d] %q is empty or invalid", label, i, tag)
		}
		if seen[tag] {
			return nil, fmt.Errorf("%s repeats %q", label, tag)
		}
		seen[tag] = true
		out[i] = tag
	}
	sort.Strings(out)
	return out, nil
}

func cohesionReleaseTags(version string) ([]string, error) {
	const prefix = "go1."
	if !strings.HasPrefix(version, prefix) {
		return nil, fmt.Errorf("goBuild.goVersion %q must have go1.N form", version)
	}
	minorText := strings.TrimPrefix(version, prefix)
	if minorText == "" || strings.Contains(minorText, ".") {
		return nil, fmt.Errorf("goBuild.goVersion %q must have go1.N form", version)
	}
	minor, err := strconv.Atoi(minorText)
	if err != nil || minor < 1 {
		return nil, fmt.Errorf("goBuild.goVersion %q must have go1.N form", version)
	}
	tags := make([]string, 0, minor)
	for release := 1; release <= minor; release++ {
		tags = append(tags, fmt.Sprintf("go1.%d", release))
	}
	return tags, nil
}

// supportedCohesionGoTargets mirrors `go tool dist list` from the module's
// Go 1.25 toolchain. Schema/version review is required when that registry grows.
var supportedCohesionGoTargets = map[string]bool{
	"aix/ppc64":   true,
	"android/386": true, "android/amd64": true, "android/arm": true, "android/arm64": true,
	"darwin/amd64": true, "darwin/arm64": true,
	"dragonfly/amd64": true,
	"freebsd/386":     true, "freebsd/amd64": true, "freebsd/arm": true, "freebsd/arm64": true, "freebsd/riscv64": true,
	"illumos/amd64": true,
	"ios/amd64":     true, "ios/arm64": true,
	"js/wasm":   true,
	"linux/386": true, "linux/amd64": true, "linux/arm": true, "linux/arm64": true,
	"linux/loong64": true, "linux/mips": true, "linux/mips64": true, "linux/mips64le": true,
	"linux/mipsle": true, "linux/ppc64": true, "linux/ppc64le": true, "linux/riscv64": true, "linux/s390x": true,
	"netbsd/386": true, "netbsd/amd64": true, "netbsd/arm": true, "netbsd/arm64": true,
	"openbsd/386": true, "openbsd/amd64": true, "openbsd/arm": true, "openbsd/arm64": true,
	"openbsd/ppc64": true, "openbsd/riscv64": true,
	"plan9/386": true, "plan9/amd64": true, "plan9/arm": true,
	"solaris/amd64": true,
	"wasip1/wasm":   true,
	"windows/386":   true, "windows/amd64": true, "windows/arm64": true,
}

func validCohesionBuildTag(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') &&
			(r < '0' || r > '9') && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

func validCohesionSymbolName(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) < 1 || len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if !token.IsIdentifier(part) {
			return false
		}
	}
	return true
}

func validateCohesionRelativePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("path is empty")
	}
	if filepath.IsAbs(path) {
		return errors.New("path must be root-relative")
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("path escapes repository root")
	}
	if filepath.ToSlash(clean) != filepath.ToSlash(path) {
		return errors.New("path must be clean and canonical")
	}
	return nil
}

func resolveCohesionAnchor(root, relative string) (string, error) {
	if err := validateCohesionRelativePath(filepath.ToSlash(relative)); err != nil {
		return "", err
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	pathResolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootResolved, pathResolved)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s resolves outside repository root", filepath.ToSlash(relative))
	}
	return pathResolved, nil
}

func validateCohesionTestRef(ref CohesionTestRef) (string, error) {
	file := filepath.ToSlash(strings.TrimSpace(ref.File))
	fn := strings.TrimSpace(ref.Func)
	if err := validateCohesionRelativePath(file); err != nil {
		return "", fmt.Errorf("test file %q: %w", file, err)
	}
	if !strings.HasSuffix(file, "_test.go") {
		return "", fmt.Errorf("test file %q must end in _test.go", file)
	}
	if fn == "" {
		return "", errors.New("test function is empty")
	}
	return cohesionTestKey(file, fn), nil
}

func cohesionTestKey(file, fn string) string {
	return filepath.ToSlash(strings.TrimSpace(file)) + ":" + strings.TrimSpace(fn)
}

func cohesionSymbolKey(ref CohesionSymbolRef) string {
	return filepath.ToSlash(strings.TrimSpace(ref.Package)) + ":" + strings.TrimSpace(ref.Name)
}

func hasTagRef(refs []TagRef, test CohesionTestRef) bool {
	wantFile := filepath.ToSlash(strings.TrimSpace(test.File))
	wantFunc := strings.TrimSpace(test.Func)
	for _, ref := range refs {
		if ref.File == wantFile && ref.Func == wantFunc {
			return true
		}
	}
	return false
}

func (r *CohesionReport) add(severity, code, component, operation, message string) {
	r.Findings = append(r.Findings, CohesionFinding{
		Severity: severity, Code: code, Component: component,
		Operation: operation, Message: message,
	})
}

func (r *CohesionReport) sortFindings() {
	sort.Slice(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if cohesionSeverityRank(a.Severity) != cohesionSeverityRank(b.Severity) {
			return cohesionSeverityRank(a.Severity) < cohesionSeverityRank(b.Severity)
		}
		if a.Component != b.Component {
			return a.Component < b.Component
		}
		if a.Operation != b.Operation {
			return a.Operation < b.Operation
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
}

func cohesionSeverityRank(severity string) int {
	if severity == CohesionError {
		return 0
	}
	return 1
}

func (r CohesionReport) counts() (errorsCount, warningsCount int) {
	for _, finding := range r.Findings {
		switch finding.Severity {
		case CohesionError:
			errorsCount++
		case CohesionWarning:
			warningsCount++
		}
	}
	return errorsCount, warningsCount
}

// HasErrors reports whether the receipt has any invalid machine-checkable claim.
func (r CohesionReport) HasErrors() bool {
	errorsCount, _ := r.counts()
	return errorsCount > 0
}

// HasWarnings reports whether the inspection found advisory drift or debt.
func (r CohesionReport) HasWarnings() bool {
	_, warningsCount := r.counts()
	return warningsCount > 0
}

// WriteText renders a stable human-readable inspection report.
func (r CohesionReport) WriteText(w io.Writer) error {
	if w == nil {
		w = io.Discard
	}
	if _, err := fmt.Fprintf(w, "cohesion-check: %d %s, %d %s, %d %s\n",
		len(r.Components), plural(len(r.Components), "component", "components"),
		r.IslandCount, plural(r.IslandCount, "island", "islands"),
		r.OperationCount, plural(r.OperationCount, "operation", "operations")); err != nil {
		return err
	}
	tags := "(none)"
	if len(r.GoBuild.Tags) > 0 {
		tags = strings.Join(r.GoBuild.Tags, ",")
	}
	toolTags := "(none)"
	if len(r.GoBuild.ToolTags) > 0 {
		toolTags = strings.Join(r.GoBuild.ToolTags, ",")
	}
	if _, err := fmt.Fprintf(w, "cohesion-check: Go build %s/%s, cgo=%t, go=%s, compiler=%s, tags=%s, tool-tags=%s\n",
		r.GoBuild.GOOS, r.GoBuild.GOARCH, r.GoBuild.CgoEnabled,
		r.GoBuild.GoVersion, r.GoBuild.Compiler, tags, toolTags); err != nil {
		return err
	}
	for _, component := range r.Components {
		if _, err := fmt.Fprintf(w, "  %s: %s (%d %s, %d %s)\n",
			component.Component, component.Status,
			component.Islands, plural(component.Islands, "island", "islands"),
			component.Operations, plural(component.Operations, "operation", "operations")); err != nil {
			return err
		}
	}
	for _, finding := range r.Findings {
		where := finding.Component
		if finding.Operation != "" {
			where += "/" + finding.Operation
		}
		if where != "" {
			where += ": "
		}
		if _, err := fmt.Fprintf(w, "  %s [%s] %s%s\n", strings.ToUpper(finding.Severity), finding.Code, where, finding.Message); err != nil {
			return err
		}
	}
	errorsCount, warningsCount := r.counts()
	if errorsCount == 0 {
		_, err := fmt.Fprintf(w, "cohesion-check: receipt valid (%d advisories)\n", warningsCount)
		return err
	}
	_, err := fmt.Fprintf(w, "cohesion-check: receipt invalid (%d errors, %d advisories)\n", errorsCount, warningsCount)
	return err
}

func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}
