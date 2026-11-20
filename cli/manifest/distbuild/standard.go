package distbuild

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/tarantool/tt/sdk/v3"
	"github.com/tarantool/tt/sdk/v3/log"
	"github.com/tarantool/tt/v3/cli/manifest"
)

const (
	// standardPackage is the package of the core, relative to its module,
	// that lists the standard modules.
	standardPackage = "standard"
	// standardFunc is the function of standardPackage that returns them.
	standardFunc = "Modules"
	// standardSince is the first release of tt's core with standardPackage.
	standardSince = "v3.0.0-alpha.3"
)

// errNoStandardList reports a standard package the build cannot read the
// modules from.
var errNoStandardList = errors.New("want func " + standardFunc + " that returns " +
	"a map literal with string keys and <import>.<constructor> values")

var (
	// errNotImported reports a constructor of a package the file of the
	// list imports under no such name.
	errNotImported = errors.New("is no package the file imports by that name")
	// errInternalPackage reports a constructor in an internal package,
	// which the main package of a distribution may not import.
	errInternalPackage = errors.New("is an internal package, which a distribution " +
		"cannot import")
)

// standardModule is a module the core's package standard lists.
type standardModule struct {
	// Name is the key it is listed under.
	Name string
	// Package is the import path of the constructor's package.
	Package string
	// Symbol is the constructor.
	Symbol string
}

// standardSet is what a distribution takes of the standard modules of its
// core.
type standardSet struct {
	// Enabled is set when the distribution takes the standard modules.
	Enabled bool
	// Modules are the standard modules the binary has, in the order of
	// their names: the core's list but the excluded ones and those of
	// [dependencies], which take their place.
	Modules []standardModule
}

// names returns the names of the standard modules the binary has.
func (s standardSet) names() []string {
	if len(s.Modules) == 0 {
		return nil
	}

	names := make([]string, 0, len(s.Modules))

	for _, mod := range s.Modules {
		names = append(names, mod.Name)
	}

	return names
}

// withModules returns the modules main is generated with, in the order of
// their names: the standard ones the binary has, imported from their
// packages, and modules.
func (s standardSet) withModules(modules []module) []module {
	all := make([]module, 0, len(s.Modules)+len(modules))

	for _, mod := range s.Modules {
		all = append(all, module{Name: mod.Name, Package: mod.Package, Symbol: mod.Symbol})
	}

	all = append(all, modules...)

	slices.SortFunc(all, func(a, b module) int { return cmp.Compare(a.Name, b.Name) })

	return all
}

// resolveStandard reads the standard modules of the core and sets the
// excluded ones and those [dependencies] takes the place of aside. A
// distribution with standard = false takes none.
func (r *resolver) resolveStandard(
	ctx context.Context, dist *manifest.Distribution, resolved core,
) (standardSet, error) {
	opts := dist.Distribution
	if !opts.StandardValue() {
		return standardSet{Enabled: false, Modules: nil}, nil
	}

	dir, err := r.coreSource(ctx, resolved)
	if err != nil {
		return standardSet{}, fmt.Errorf("distribution.standard: %w", err)
	}

	listed, found, err := readStandard(filepath.Join(dir, standardPackage))
	if err != nil {
		return standardSet{}, fmt.Errorf("distribution.standard: %w", err)
	}

	if !found {
		return standardSet{}, sdk.Errorf(sdk.ExitFailure, "distribution.standard: "+
			"the core %s has no package %s/%s that lists the standard modules: "+
			"standard needs core %s or later; set standard = false and list the "+
			"modules in [dependencies]", describeCore(resolved), resolved.Module,
			standardPackage, standardSince)
	}

	names := standardSet{Enabled: true, Modules: listed}.names()

	for _, name := range opts.Exclude {
		if name != manifest.BuiltinModule && !slices.Contains(names, name) {
			return standardSet{}, sdk.Errorf(sdk.ExitFailure, "distribution.exclude: "+
				"%q is not a standard module of the core %s: they are %s",
				name, describeCore(resolved),
				strings.Join(slices.Concat(names, []string{manifest.BuiltinModule}), ", "))
		}
	}

	set := standardSet{Enabled: true, Modules: nil}

	for _, mod := range listed {
		switch {
		case slices.Contains(opts.Exclude, mod.Name):
		case hasDependency(dist, mod.Name):
			log.Warnf("dependencies.%s takes the place of the standard module %s of the "+
				"core: add %q to distribution.exclude to say so, or drop the dependency "+
				"to take the core's", mod.Name, mod.Name, mod.Name)
		default:
			set.Modules = append(set.Modules, mod)
		}
	}

	return set, nil
}

// coreSource returns the directory of the source of the core: the local
// core, or the copy of the versioned one in the module cache, downloaded.
func (r *resolver) coreSource(ctx context.Context, resolved core) (string, error) {
	if resolved.Dir != "" {
		return resolved.Dir, nil
	}

	return downloadModule(ctx, r.gocmd, r.work, r.env, resolved.Module, resolved.Version)
}

// readStandard reads the standard modules from the source of the package in
// dir, without building it: the map literal its func Modules returns, a
// constructor of an imported package under each name, in the order of the
// names. It reports false when there is no such package.
func readStandard(dir string) ([]standardModule, bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, sdk.Errorf(sdk.ExitSystem, "reading %s: %w", dir, err)
	}

	fset := token.NewFileSet()

	for _, entry := range entries {
		name := entry.Name()
		if !isGoFile(entry) || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, parseErr := parser.ParseFile(fset, filepath.Join(dir, name), nil,
			parser.SkipObjectResolution)
		if parseErr != nil {
			return nil, true, sdk.Errorf(sdk.ExitFailure, "reading the standard modules: %w",
				parseErr)
		}

		fn := findFunc(file, standardFunc)
		if fn == nil {
			continue
		}

		modules, listErr := standardList(file, fn)
		if listErr != nil {
			return nil, true, sdk.Errorf(sdk.ExitFailure, "reading the standard modules "+
				"from %s: %w", filepath.Join(dir, name), listErr)
		}

		slices.SortFunc(modules, func(a, b standardModule) int {
			return cmp.Compare(a.Name, b.Name)
		})

		return modules, true, nil
	}

	return nil, true, sdk.Errorf(sdk.ExitFailure, "reading the standard modules from %s: %w",
		dir, errNoStandardList)
}

// findFunc returns the function name of file, not a method; nil when there
// is none.
func findFunc(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == name && fn.Body != nil {
			return fn
		}
	}

	return nil
}

// standardList returns the modules of the map literal fn of file returns,
// its body one return statement: a string literal key and a selector of an
// exported name of a package file imports for each.
func standardList(file *ast.File, fn *ast.FuncDecl) ([]standardModule, error) {
	lit := returnedMap(fn)
	if lit == nil {
		return nil, errNoStandardList
	}

	imports, err := importedPackages(file)
	if err != nil {
		return nil, err
	}

	modules := make([]standardModule, 0, len(lit.Elts))

	for _, elt := range lit.Elts {
		mod, entryErr := standardEntry(elt, imports)
		if entryErr != nil {
			return nil, entryErr
		}

		modules = append(modules, mod)
	}

	return modules, nil
}

// standardEntry returns the module of one entry of the map literal of
// Modules, whose file imports imports.
func standardEntry(elt ast.Expr, imports map[string]string) (standardModule, error) {
	entry, isKV := elt.(*ast.KeyValueExpr)
	if !isKV {
		return standardModule{}, errNoStandardList
	}

	key, isLit := entry.Key.(*ast.BasicLit)
	if !isLit || key.Kind != token.STRING {
		return standardModule{}, errNoStandardList
	}

	name, err := strconv.Unquote(key.Value)
	if err != nil {
		return standardModule{}, errNoStandardList
	}

	sel, isSel := entry.Value.(*ast.SelectorExpr)
	if !isSel || !sel.Sel.IsExported() {
		return standardModule{}, fmt.Errorf("%q: %w", name, errNoStandardList)
	}

	pkg, isIdent := sel.X.(*ast.Ident)
	if !isIdent {
		return standardModule{}, fmt.Errorf("%q: %w", name, errNoStandardList)
	}

	importPath, imported := imports[pkg.Name]
	if !imported {
		return standardModule{}, fmt.Errorf("%q: %s %w: import it as %s",
			name, pkg.Name, errNotImported, pkg.Name)
	}

	if isInternal(importPath) {
		return standardModule{}, fmt.Errorf("%q: %s %w", name, importPath, errInternalPackage)
	}

	return standardModule{Name: name, Package: importPath, Symbol: sel.Sel.Name}, nil
}

// returnedMap returns the map literal fn returns, its body one return
// statement; nil when it is not one.
func returnedMap(fn *ast.FuncDecl) *ast.CompositeLit {
	if len(fn.Body.List) != 1 {
		return nil
	}

	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return nil
	}

	lit, ok := ret.Results[0].(*ast.CompositeLit)
	if !ok {
		return nil
	}

	if _, isMap := lit.Type.(*ast.MapType); !isMap {
		return nil
	}

	return lit
}

// importedPackages maps the names file refers to its imports by to their
// paths: the name an import gives, or else the last element of the path
// without a major version suffix, which is the name of the package by
// convention.
func importedPackages(file *ast.File) (map[string]string, error) {
	imports := map[string]string{}

	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("import %s: %w", spec.Path.Value, err)
		}

		name := path.Base(importPath)
		if majorSuffixRe.MatchString("/"+name) && path.Dir(importPath) != "." {
			name = path.Base(path.Dir(importPath))
		}

		if spec.Name != nil {
			name = spec.Name.Name
		}

		imports[name] = importPath
	}

	return imports, nil
}

// isInternal reports whether importPath is an internal package, which only
// the tree it is internal to may import.
func isInternal(importPath string) bool {
	return slices.Contains(strings.Split(importPath, "/"), "internal")
}

// hasDependency reports whether [dependencies] lists name.
func hasDependency(dist *manifest.Distribution, name string) bool {
	_, ok := dist.Dependencies[name]

	return ok
}

// describeCore names the core in an error: its version, or its directory.
func describeCore(resolved core) string {
	if resolved.Dir != "" {
		return resolved.Path
	}

	return resolved.Module + "@" + resolved.Version
}

// checkLockedStandard checks that a build from the lock takes the standard
// modules the lock names.
func checkLockedStandard(lock *manifest.DistributionLock, set standardSet) error {
	names := set.names()
	if slices.Equal(lock.Core.Standard, names) {
		return nil
	}

	return sdk.WithCode(sdk.ExitFailure, fmt.Errorf("%w: it names the standard modules "+
		"[%s], the build takes [%s]; rebuild without --locked to update it", errStaleLock,
		strings.Join(lock.Core.Standard, ", "), strings.Join(names, ", ")))
}

// logStandard tells what standard modules the distribution has.
func logStandard(set standardSet) {
	if names := set.names(); len(names) > 0 {
		log.Infof("  standard %s (with the core)", strings.Join(names, ", "))
	}
}
