package distbuild

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log"
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
// names of the modules from.
var errNoStandardList = errors.New("want func " + standardFunc + " that returns " +
	"a map literal with string keys")

// standardSet is what a distribution takes of the standard modules of its
// core.
type standardSet struct {
	// Enabled is set when the distribution takes the standard modules:
	// main is built from the core's list of them.
	Enabled bool
	// Modules are the names of the standard modules the binary has, in
	// order: the core's list but the excluded ones and those of
	// [dependencies], which take their place.
	Modules []string
	// Excluded are the names of the core's list main deletes from it, in
	// order.
	Excluded []string
}

// resolveStandard reads the names of the standard modules of the core and
// sets the excluded ones and those [dependencies] takes the place of
// aside. A distribution with standard = false takes none.
func (r *resolver) resolveStandard(
	ctx context.Context, dist *manifest.Distribution, resolved core,
) (standardSet, error) {
	opts := dist.Distribution
	if !opts.StandardValue() {
		return standardSet{Enabled: false, Modules: nil, Excluded: nil}, nil
	}

	dir, err := r.coreSource(ctx, resolved)
	if err != nil {
		return standardSet{}, fmt.Errorf("distribution.standard: %w", err)
	}

	names, found, err := readStandard(filepath.Join(dir, standardPackage))
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

	for _, name := range opts.Exclude {
		if name != manifest.BuiltinModule && !slices.Contains(names, name) {
			return standardSet{}, sdk.Errorf(sdk.ExitFailure, "distribution.exclude: "+
				"%q is not a standard module of the core %s: they are %s",
				name, describeCore(resolved),
				strings.Join(slices.Concat(names, []string{manifest.BuiltinModule}), ", "))
		}
	}

	set := standardSet{Enabled: true, Modules: nil, Excluded: nil}

	for _, name := range names {
		switch {
		case slices.Contains(opts.Exclude, name):
			set.Excluded = append(set.Excluded, name)
		case hasDependency(dist, name):
			log.Warnf("dependencies.%s takes the place of the standard module %s of the "+
				"core: add %q to distribution.exclude to say so, or drop the dependency "+
				"to take the core's", name, name, name)
		default:
			set.Modules = append(set.Modules, name)
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

// readStandard reads the names of the standard modules from the source of
// the package in dir, without building it: the keys of the map literal
// its func Modules returns, in order. It reports false when there is no
// such package.
func readStandard(dir string) ([]string, bool, error) {
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

		names, ok := mapLiteralKeys(fn)
		if !ok {
			return nil, true, sdk.Errorf(sdk.ExitFailure, "reading the standard modules "+
				"from %s: %w", filepath.Join(dir, name), errNoStandardList)
		}

		slices.Sort(names)

		return names, true, nil
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

// mapLiteralKeys returns the keys of the map literal fn returns, its body
// one return statement, when they are all string literals.
func mapLiteralKeys(fn *ast.FuncDecl) ([]string, bool) {
	if len(fn.Body.List) != 1 {
		return nil, false
	}

	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return nil, false
	}

	lit, ok := ret.Results[0].(*ast.CompositeLit)
	if !ok {
		return nil, false
	}

	if _, isMap := lit.Type.(*ast.MapType); !isMap {
		return nil, false
	}

	names := make([]string, 0, len(lit.Elts))

	for _, elt := range lit.Elts {
		kv, isKV := elt.(*ast.KeyValueExpr)
		if !isKV {
			return nil, false
		}

		key, isLit := kv.Key.(*ast.BasicLit)
		if !isLit || key.Kind != token.STRING {
			return nil, false
		}

		name, err := strconv.Unquote(key.Value)
		if err != nil {
			return nil, false
		}

		names = append(names, name)
	}

	return names, true
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
	if slices.Equal(lock.Core.Standard, set.Modules) {
		return nil
	}

	return sdk.WithCode(sdk.ExitFailure, fmt.Errorf("%w: it names the standard modules "+
		"[%s], the build takes [%s]; rebuild without --locked to update it", errStaleLock,
		strings.Join(lock.Core.Standard, ", "), strings.Join(set.Modules, ", ")))
}

// logStandard tells what standard modules the distribution has.
func logStandard(set standardSet) {
	if set.Enabled && len(set.Modules) > 0 {
		log.Infof("  standard %s (with the core)", strings.Join(set.Modules, ", "))
	}
}
