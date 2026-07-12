package tracecheck

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type goSymbolInfo struct {
	Exported bool
}

type goPackageInventory struct {
	Symbols  map[string]goSymbolInfo
	Exported []string
}

type pendingGoVariable struct {
	Name         string
	Direct       bool
	Target       string
	FunctionType string
}

func scanGoPackage(root, packagePath string, buildContext build.Context) (goPackageInventory, error) {
	if err := validateCohesionRelativePath(packagePath); err != nil {
		return goPackageInventory{}, err
	}
	dir, err := resolveCohesionAnchor(root, packagePath)
	if err != nil {
		return goPackageInventory{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return goPackageInventory{}, err
	}
	inventory := goPackageInventory{Symbols: map[string]goSymbolInfo{}}
	declaredFunctions := map[string]bool{}
	functionTypes := map[string]bool{}
	var pendingVariables []pendingGoVariable
	fset := token.NewFileSet()
	nonTestFiles := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		matched, err := buildContext.MatchFile(dir, entry.Name())
		if err != nil {
			return goPackageInventory{}, err
		}
		if !matched {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return goPackageInventory{}, fmt.Errorf("parse %s: %w", filepath.ToSlash(filepath.Join(packagePath, entry.Name())), err)
		}
		if goImportsC(file) && !buildContext.CgoEnabled {
			continue
		}
		nonTestFiles++
		for _, declaration := range file.Decls {
			switch typed := declaration.(type) {
			case *ast.FuncDecl:
				name := typed.Name.Name
				exported := ast.IsExported(name)
				if typed.Recv != nil && len(typed.Recv.List) > 0 {
					receiver := goReceiverName(typed.Recv.List[0].Type)
					if receiver == "" {
						continue
					}
					name = receiver + "." + name
				} else {
					declaredFunctions[name] = true
				}
				addGoSymbol(&inventory, name, exported)
			case *ast.GenDecl:
				if typed.Tok == token.VAR {
					for _, spec := range typed.Specs {
						valueSpec, ok := spec.(*ast.ValueSpec)
						if !ok {
							continue
						}
						for i, variable := range valueSpec.Names {
							pending := pendingGoVariable{Name: variable.Name}
							if _, ok := valueSpec.Type.(*ast.FuncType); ok {
								pending.Direct = true
							} else if ident, ok := valueSpec.Type.(*ast.Ident); ok {
								pending.FunctionType = ident.Name
							}
							var value ast.Expr
							if len(valueSpec.Values) == len(valueSpec.Names) {
								value = valueSpec.Values[i]
							} else if len(valueSpec.Names) == 1 && len(valueSpec.Values) == 1 {
								value = valueSpec.Values[0]
							}
							switch expression := value.(type) {
							case *ast.FuncLit:
								pending.Direct = true
							case *ast.Ident:
								pending.Target = expression.Name
							}
							pendingVariables = append(pendingVariables, pending)
						}
					}
					continue
				}
				if typed.Tok != token.TYPE {
					continue
				}
				for _, spec := range typed.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if _, ok := typeSpec.Type.(*ast.FuncType); ok {
						functionTypes[typeSpec.Name.Name] = true
						continue
					}
					iface, ok := typeSpec.Type.(*ast.InterfaceType)
					if !ok {
						continue
					}
					for _, method := range iface.Methods.List {
						if _, ok := method.Type.(*ast.FuncType); !ok {
							continue
						}
						for _, methodName := range method.Names {
							name := typeSpec.Name.Name + "." + methodName.Name
							addGoSymbol(&inventory, name, ast.IsExported(methodName.Name))
						}
					}
				}
			}
		}
	}
	callableIdentifiers := make(map[string]bool, len(declaredFunctions)+len(pendingVariables))
	for name := range declaredFunctions {
		callableIdentifiers[name] = true
	}
	remaining := append([]pendingGoVariable(nil), pendingVariables...)
	for len(remaining) > 0 {
		progress := false
		next := make([]pendingGoVariable, 0, len(remaining))
		for _, variable := range remaining {
			callable := variable.Direct || functionTypes[variable.FunctionType] || callableIdentifiers[variable.Target]
			if !callable {
				next = append(next, variable)
				continue
			}
			addGoSymbol(&inventory, variable.Name, ast.IsExported(variable.Name))
			callableIdentifiers[variable.Name] = true
			progress = true
		}
		if !progress {
			break
		}
		remaining = next
	}
	if nonTestFiles == 0 {
		return goPackageInventory{}, fmt.Errorf("no non-test Go files found")
	}
	for name, info := range inventory.Symbols {
		if info.Exported {
			inventory.Exported = append(inventory.Exported, name)
		}
	}
	sort.Strings(inventory.Exported)
	return inventory, nil
}

func addGoSymbol(inventory *goPackageInventory, name string, exported bool) {
	previous := inventory.Symbols[name]
	inventory.Symbols[name] = goSymbolInfo{Exported: previous.Exported || exported}
}

func goReceiverName(expr ast.Expr) string {
	switch receiver := expr.(type) {
	case *ast.Ident:
		return receiver.Name
	case *ast.StarExpr:
		return goReceiverName(receiver.X)
	case *ast.IndexExpr:
		return goReceiverName(receiver.X)
	case *ast.IndexListExpr:
		return goReceiverName(receiver.X)
	case *ast.ParenExpr:
		return goReceiverName(receiver.X)
	default:
		return ""
	}
}

func goTestExists(root string, ref CohesionTestRef, buildContext build.Context) (bool, error) {
	if _, err := validateCohesionTestRef(ref); err != nil {
		return false, err
	}
	path, err := resolveCohesionAnchor(root, ref.File)
	if err != nil {
		return false, err
	}
	dir, base := filepath.Split(path)
	matched, err := buildContext.MatchFile(dir, base)
	if err != nil {
		return false, err
	}
	if !matched {
		return false, fmt.Errorf("%s is excluded by current build constraints", ref.File)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return false, err
	}
	if goImportsC(file) {
		return false, fmt.Errorf("%s imports C; cgo is not supported in Go test files", ref.File)
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != ref.Func {
			continue
		}
		if err := validateRunnableGoTest(file, fn); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func goImportsC(file *ast.File) bool {
	for _, importSpec := range file.Imports {
		path, err := strconv.Unquote(importSpec.Path.Value)
		if err == nil && path == "C" {
			return true
		}
	}
	return false
}

func validateRunnableGoTest(file *ast.File, fn *ast.FuncDecl) error {
	name := fn.Name.Name
	if fn.Type.TypeParams != nil && len(fn.Type.TypeParams.List) > 0 {
		return fmt.Errorf("%s must not have type parameters", name)
	}
	switch {
	case goTestName(name, "Test"):
		if !goTestingSignature(file, fn, "T") {
			return fmt.Errorf("%s must have signature func(*testing.T)", name)
		}
	case goTestName(name, "Fuzz"):
		if !goTestingSignature(file, fn, "F") {
			return fmt.Errorf("%s must have signature func(*testing.F)", name)
		}
	case goTestName(name, "Example"):
		if goFieldCount(fn.Type.Params) != 0 || goFieldCount(fn.Type.Results) != 0 {
			return fmt.Errorf("%s must have signature func()", name)
		}
		if !goRunnableExample(file, name) {
			return fmt.Errorf("%s has no recognized Output or Unordered output directive and will not run", name)
		}
	default:
		return fmt.Errorf("%s is not a runnable Go test, fuzz target, or example name", name)
	}
	return nil
}

func goTestName(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	if len(name) == len(prefix) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(name[len(prefix):])
	return !unicode.IsLower(r)
}

func goTestingSignature(file *ast.File, fn *ast.FuncDecl, testingType string) bool {
	if goFieldCount(fn.Type.Params) != 1 || goFieldCount(fn.Type.Results) != 0 {
		return false
	}
	field := fn.Type.Params.List[0]
	star, ok := field.Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	aliases, dotImported := goTestingImports(file)
	switch expr := star.X.(type) {
	case *ast.SelectorExpr:
		pkg, ok := expr.X.(*ast.Ident)
		return ok && aliases[pkg.Name] && expr.Sel.Name == testingType
	case *ast.Ident:
		return dotImported && expr.Name == testingType
	default:
		return false
	}
}

func goTestingImports(file *ast.File) (map[string]bool, bool) {
	aliases := map[string]bool{}
	dotImported := false
	for _, importSpec := range file.Imports {
		path, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil || path != "testing" {
			continue
		}
		if importSpec.Name == nil {
			aliases["testing"] = true
			continue
		}
		switch importSpec.Name.Name {
		case ".":
			dotImported = true
		case "_":
		default:
			aliases[importSpec.Name.Name] = true
		}
	}
	return aliases, dotImported
}

func goFieldCount(fields *ast.FieldList) int {
	if fields == nil {
		return 0
	}
	count := 0
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			count++
		} else {
			count += len(field.Names)
		}
	}
	return count
}

func goRunnableExample(file *ast.File, functionName string) bool {
	for _, example := range doc.Examples(file) {
		name := "Example" + example.Name
		if example.Suffix != "" {
			name += "_" + example.Suffix
		}
		if name == functionName {
			return example.Output != "" || example.EmptyOutput
		}
	}
	return false
}
