package distbuild

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/version"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/manifest"
)

const (
	// goWork is the file of a Go workspace, and goWorkSum the checksums the
	// go command keeps beside it.
	goWork    = "go.work"
	goWorkSum = "go.work.sum"
	// workspaceMark opens every go.work Workspace writes: it rewrites and
	// removes only a file that starts with it.
	workspaceMark = "// Written by tt binary workspace"
	// sdkDir is the directory of the core's repository with the Go modules
	// of the SDK.
	sdkDir = "sdk"
)

var (
	// errForeignWorkspace reports a go.work that Workspace did not write.
	errForeignWorkspace = errors.New("was not written by tt binary workspace")
	// errWorkspaceRemoveForce reports --remove and --force together.
	errWorkspaceRemoveForce = errors.New("--remove and --force exclude each other: " +
		"--remove deletes only a go.work tt wrote")
)

// WorkspaceOptions configure Workspace.
type WorkspaceOptions struct {
	// Manifest is the path to tt.manifest.toml.
	Manifest string
	// Core overrides [core] of the manifest, as Options.Core does; only a
	// local checkout makes a workspace.
	Core string
	// CoreDir is the directory a relative Core path is taken from.
	CoreDir string
	// Remove deletes the go.work files Workspace wrote, and their
	// go.work.sum, instead of writing them.
	Remove bool
	// Force overwrites a go.work Workspace did not write. It excludes
	// Remove.
	Force bool
	// DryRun writes and removes nothing: the result says what would be.
	DryRun bool
	// Environ is the environment of the go command. Nil: the process's.
	Environ []string

	// gocmd runs the go command; nil is the machine's.
	gocmd goCommand
}

// WorkspaceFile is a go.work Workspace wrote or removed.
type WorkspaceFile struct {
	// Path is the go.work.
	Path string
	// Content is what it holds; nil for a file removed.
	Content []byte
}

// WorkspaceResult is what Workspace did.
type WorkspaceResult struct {
	// Files are the go.work files written, or removed, in the order of
	// their paths.
	Files []WorkspaceFile
}

// workReplace is a replace directive of a go.work: a module that the
// workspace takes from a local directory.
type workReplace struct {
	// Module is the module path.
	Module string
	// Version limits the directive to that version; empty for every one.
	Version string
	// Dir is the directory, absolute.
	Dir string
}

// workSpec is the content of one go.work.
type workSpec struct {
	// Dir is the directory of the go.work, absolute.
	Dir string
	// Manifest is the distribution manifest, absolute.
	Manifest string
	// Core is the local core, absolute.
	Core string
	// Go is the go directive.
	Go string
	// Use are the directories of the workspace modules, absolute.
	Use []string
	// Replace are the modules taken from directories, in the order of
	// their paths.
	Replace []workReplace
}

// Workspace writes a Go workspace for developing the local tt modules of
// the distribution opts.Manifest describes together with its local core: a
// go.work that uses every module of [dependencies] given by a path and
// replaces, with no version, the core, every Go module of its repository
// that its go.mod replaces with a directory, every Go module under its sdk/
// and every module [replace] replaces with a directory, so that go test,
// go build and gopls in a module build it on the SDK of the checkout
// whatever version its go.mod requires, a commit not pushed yet as well.
//
// A go.work is written beside the manifest for the modules in the
// manifest's directory, and in the directory of every other module, so that
// the go command finds one from each: it looks for go.work from the current
// directory up. Each one uses every local module. A go.work there that
// Workspace did not write is refused unless opts.Force; one it wrote is
// rewritten. With opts.Remove the files are removed instead, with their
// go.work.sum, and a go.work Workspace did not write is refused; nothing is
// removed then.
func Workspace(ctx context.Context, opts WorkspaceOptions) (*WorkspaceResult, error) {
	if opts.Remove && opts.Force {
		return nil, sdk.WithCode(sdk.ExitFailure, errWorkspaceRemoveForce)
	}

	if opts.gocmd == nil {
		opts.gocmd = execGo{showOutput: false}
	}

	if opts.Environ == nil {
		opts.Environ = os.Environ()
	}

	manifestPath, err := filepath.Abs(opts.Manifest)
	if err != nil {
		return nil, sdk.Errorf(sdk.ExitSystem, "%s: %w", opts.Manifest, err)
	}

	opts.Manifest = manifestPath

	dist, err := readManifest(opts.Manifest)
	if err != nil {
		return nil, err
	}

	if opts.Core != "" {
		dist.Core = overrideCore(dist.Core, opts.Core, opts.CoreDir)
	}

	dir := filepath.Dir(opts.Manifest)
	res := resolver{
		gocmd: opts.gocmd, dir: dir, work: dir,
		env:    append(slices.Clone(opts.Environ), "GOWORK=off"),
		pinned: nil, unpinnedCore: true, runningCore: "",
	}

	modules, err := workspaceModules(ctx, &res, dist)
	if err != nil {
		return nil, err
	}

	locations := workspaceDirs(dir, modules)

	if opts.Remove {
		// Where the files are follows from the modules: no core is needed.
		return removeWorkspace(locations, opts.DryRun)
	}

	if dist.Core.Path == "" {
		return nil, sdk.Errorf(sdk.ExitFailure, "core: a workspace builds the modules on a "+
			"local checkout of the core: set [core].path, or give --core DIR; a version of "+
			"the core comes from the module cache, which has no change of yours")
	}

	spec, err := workspaceSpec(ctx, &res, dist, modules)
	if err != nil {
		return nil, err
	}

	spec.Manifest = opts.Manifest

	return writeWorkspace(spec, locations, modules, opts)
}

// workspaceModules returns the local tt modules of the distribution.
func workspaceModules(
	ctx context.Context, res *resolver, dist *manifest.Distribution,
) ([]module, error) {
	var modules []module

	for _, name := range slices.Sorted(maps.Keys(dist.Dependencies)) {
		dep := dist.Dependencies[name]
		if !dep.IsPath() {
			continue
		}

		mod, err := res.localModule(ctx, name, dep)
		if err != nil {
			return nil, fmt.Errorf("dependencies.%s: %w", name, err)
		}

		modules = append(modules, mod)
	}

	if len(modules) == 0 {
		return nil, sdk.Errorf(sdk.ExitFailure, "dependencies: no module is given by a "+
			"path: a workspace is for developing local modules")
	}

	return modules, nil
}

// workspaceDirs returns where the go.work files of modules go: the
// manifest's directory dir for the modules in it, and the directory of
// every other module, in order.
func workspaceDirs(dir string, modules []module) []string {
	var dirs []string

	for _, mod := range modules {
		loc := mod.Dir
		if within(dir, mod.Dir) {
			loc = dir
		}

		if !slices.Contains(dirs, loc) {
			dirs = append(dirs, loc)
		}
	}

	slices.Sort(dirs)

	return dirs
}

// within reports whether path is dir or a path in it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)

	return err == nil && filepath.IsLocal(rel)
}

// workspaceSpec collects what the go.work files hold: the local modules,
// the modules of the core that replace the versions they require, and the
// go directive, the newest of all their go.mod files.
func workspaceSpec(
	ctx context.Context, res *resolver, dist *manifest.Distribution, modules []module,
) (workSpec, error) {
	resolved, err := res.localCore(ctx, dist)
	if err != nil {
		return workSpec{}, err
	}

	spec := workSpec{
		Dir: "", Manifest: "", Core: resolved.Dir, Go: resolved.GoVersion, Use: nil, Replace: nil,
	}

	used := map[string]bool{}

	for _, mod := range modules {
		gomod, err := readGoMod(ctx, res.gocmd, res.work, res.env, filepath.Join(mod.Dir, goMod))
		if err != nil {
			return workSpec{}, err
		}

		spec.Use = append(spec.Use, mod.Dir)
		spec.Go = newerGo(spec.Go, gomod.Go)
		used[mod.Module] = true
	}

	replaces := []workReplace{{Module: resolved.Module, Version: "", Dir: resolved.Dir}}
	for _, repo := range resolved.Repository {
		replaces = append(replaces, workReplace{Module: repo.Module, Version: "", Dir: repo.Dir})
	}

	sdkModules, err := coreSDKModules(ctx, res, resolved.Dir)
	if err != nil {
		return workSpec{}, err
	}

	replaces = append(replaces, sdkModules...)

	for _, rep := range dist.Replacements() {
		if !rep.IsPath() {
			log.Warnf("replace.%s: a module version, which go.work does not take: "+
				"go test of a module uses what its go.mod requires", strconv.Quote(rep.Key))

			continue
		}

		replaces = append(replaces, workReplace{
			Module: rep.Module, Version: rep.Version, Dir: res.abs(rep.Path),
		})
	}

	seen := map[string]bool{}

	for _, rep := range replaces {
		key := rep.Module + "@" + rep.Version
		if used[rep.Module] || seen[key] {
			continue
		}

		seen[key] = true

		gomod, err := readGoMod(ctx, res.gocmd, res.work, res.env, filepath.Join(rep.Dir, goMod))
		if err != nil {
			return workSpec{}, err
		}

		spec.Go = newerGo(spec.Go, gomod.Go)
		spec.Replace = append(spec.Replace, rep)
	}

	slices.SortFunc(spec.Replace, func(a, b workReplace) int {
		return strings.Compare(a.Module+" "+a.Version, b.Module+" "+b.Version)
	})

	return spec, nil
}

// coreSDKModules returns the Go modules under sdk/ of the core in dir: every
// directory with a go.mod, but those the go command leaves out of a module
// - testdata, vendor and the names that start with . or _ - and what is
// under them.
func coreSDKModules(ctx context.Context, res *resolver, dir string) ([]workReplace, error) {
	var dirs []string

	root := filepath.Join(dir, sdkDir)

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path == root && errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipDir
			}

			return err
		}

		if !entry.IsDir() {
			return nil
		}

		name := entry.Name()
		if path != root && (name == "testdata" || name == vendorDir ||
			strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
			return filepath.SkipDir
		}

		_, statErr := os.Stat(filepath.Join(path, goMod))
		if statErr == nil {
			dirs = append(dirs, path)
		}

		return nil
	})
	if err != nil {
		return nil, sdk.Errorf(sdk.ExitSystem, "listing the SDK of the core: %w", err)
	}

	replaces := make([]workReplace, 0, len(dirs))

	for _, modDir := range dirs {
		gomod, err := readGoMod(ctx, res.gocmd, res.work, res.env, filepath.Join(modDir, goMod))
		if err != nil {
			return nil, err
		}

		replaces = append(replaces,
			workReplace{Module: gomod.Module.Path, Version: "", Dir: modDir})
	}

	return replaces, nil
}

// newerGo returns the newer of two go directives; an empty one is older
// than any.
func newerGo(current, other string) string {
	if !version.IsValid("go" + other) {
		return current
	}

	if !version.IsValid("go"+current) || version.Compare("go"+other, "go"+current) > 0 {
		return other
	}

	return current
}

// renderWorkspace returns the go.work spec describes for its directory,
// with the paths relative to it where they can be.
func renderWorkspace(spec workSpec) []byte {
	var buf bytes.Buffer

	_, _ = fmt.Fprintf(&buf, "%s: the local modules of the distribution\n"+
		"// %s on the SDK of the core %s,\n"+
		"// whatever their go.mod requires. tt binary workspace --remove deletes it.\n"+
		"// Keep it out of git.\n\n",
		workspaceMark, workPath(spec.Dir, spec.Manifest), workPath(spec.Dir, spec.Core))

	if spec.Go != "" {
		_, _ = fmt.Fprintf(&buf, "go %s\n\n", spec.Go)
	}

	buf.WriteString("use (\n")

	for _, dir := range spec.Use {
		_, _ = fmt.Fprintf(&buf, "\t%s\n", workPath(spec.Dir, dir))
	}

	buf.WriteString(")\n")

	if len(spec.Replace) == 0 {
		return buf.Bytes()
	}

	buf.WriteString("\nreplace (\n")

	for _, rep := range spec.Replace {
		old := rep.Module
		if rep.Version != "" {
			old += " " + rep.Version
		}

		_, _ = fmt.Fprintf(&buf, "\t%s => %s\n", old, workPath(spec.Dir, rep.Dir))
	}

	buf.WriteString(")\n")

	return buf.Bytes()
}

// workPath returns path as a go.work in dir names it: relative to dir, with
// ./ or ../ in front, when it can be, absolute otherwise, and quoted when
// the go command would not read it bare.
//
// The go command joins a relative path to the directory of the go.work as
// it finds it, which may be with the symbolic links of its parents resolved
// or not: on macOS the temporary directories are under /var, a link to
// /private/var. A path is relative only when it is the same both ways.
func workPath(dir, path string) string {
	name := path

	rel, err := filepath.Rel(dir, path)
	if err == nil && rel == physicalRel(dir, path) {
		name = filepath.ToSlash(rel)
		if name != "." && name != ".." && !strings.HasPrefix(name, "../") {
			name = "./" + name
		}
	}

	if mustQuote(name) {
		return strconv.Quote(name)
	}

	return name
}

// physicalRel returns path relative to dir, both with their symbolic links
// resolved, each as it is when it is not there to resolve; "" when path is
// not relative to dir.
func physicalRel(dir, path string) string {
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		realDir = dir
	}

	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		realPath = path
	}

	rel, err := filepath.Rel(realDir, realPath)
	if err != nil {
		return ""
	}

	return rel
}

// mustQuote reports whether the go command reads s in a go.work only
// quoted, as golang.org/x/mod/modfile tells.
func mustQuote(s string) bool {
	if s == "" || strings.Contains(s, "//") || strings.Contains(s, "/*") {
		return true
	}

	return strings.ContainsFunc(s, func(r rune) bool {
		return strings.ContainsRune(" \"'`()[]{},", r) || !unicode.IsPrint(r)
	})
}

// writeWorkspace writes the go.work of spec in each directory of dirs,
// after checking that none of them is a foreign one.
func writeWorkspace(
	spec workSpec, dirs []string, modules []module, opts WorkspaceOptions,
) (*WorkspaceResult, error) {
	result := &WorkspaceResult{Files: nil}

	for _, loc := range dirs {
		path := filepath.Join(loc, goWork)

		own, err := ownWorkspace(path)
		if err != nil {
			return nil, err
		}

		if !own && !opts.Force {
			return nil, sdk.Errorf(sdk.ExitFailure, "%s %w: move it away, or overwrite it "+
				"with --force", path, errForeignWorkspace)
		}

		spec.Dir = loc
		result.Files = append(result.Files,
			WorkspaceFile{Path: path, Content: renderWorkspace(spec)})
	}

	if !opts.DryRun {
		for _, file := range result.Files {
			err := writeFileAtomic(file.Path, file.Content)
			if err != nil {
				return nil, sdk.Errorf(sdk.ExitSystem, "writing %s: %w", file.Path, err)
			}
		}
	}

	warnWorkspace(dirs, modules, opts.Environ)

	return result, nil
}

// ownWorkspace reports whether the go.work at path is absent or one
// Workspace wrote.
func ownWorkspace(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}

	if err != nil {
		return false, sdk.Errorf(sdk.ExitSystem, "reading %s: %w", path, err)
	}

	return bytes.HasPrefix(data, []byte(workspaceMark)), nil
}

// removeWorkspace removes the go.work in each directory of dirs that is
// there, and its go.work.sum, after checking that Workspace wrote every
// one of them.
func removeWorkspace(dirs []string, dryRun bool) (*WorkspaceResult, error) {
	result := &WorkspaceResult{Files: nil}

	for _, loc := range dirs {
		path := filepath.Join(loc, goWork)

		_, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		own, err := ownWorkspace(path)
		if err != nil {
			return nil, err
		}

		if !own {
			return nil, sdk.Errorf(sdk.ExitFailure, "%s %w: it is left as it is", path,
				errForeignWorkspace)
		}

		result.Files = append(result.Files, WorkspaceFile{Path: path, Content: nil})
	}

	if dryRun {
		return result, nil
	}

	for _, file := range result.Files {
		sum := filepath.Join(filepath.Dir(file.Path), goWorkSum)

		for _, name := range []string{file.Path, sum} {
			err := os.Remove(name)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, sdk.Errorf(sdk.ExitSystem, "removing %s: %w", name, err)
			}
		}
	}

	return result, nil
}

// warnWorkspace warns of what keeps the go command from the go.work files
// in dirs: GOWORK naming another file or off, and a go.work between a
// module and the one written for it, which the go command finds first.
func warnWorkspace(dirs []string, modules []module, environ []string) {
	if value, ok := lookupEnv(environ, "GOWORK"); ok && value != "" && value != "auto" {
		path, _ := filepath.Abs(value)
		if !slices.ContainsFunc(dirs, func(loc string) bool {
			return filepath.Join(loc, goWork) == path
		}) {
			log.Warnf("GOWORK=%s: the go command uses that, not the go.work written here; "+
				"unset GOWORK", value)
		}
	}

	for _, mod := range modules {
		for _, loc := range dirs {
			if !within(loc, mod.Dir) {
				continue
			}

			for parent := mod.Dir; parent != loc; parent = filepath.Dir(parent) {
				other := filepath.Join(parent, goWork)

				_, err := os.Stat(other)
				if err == nil {
					log.Warnf("%s: go commands in %s use it, not %s", other, mod.Path,
						filepath.Join(loc, goWork))

					break
				}
			}
		}
	}
}

// lookupEnv returns the value of the variable name in environ, the last
// one when it is set more than once, as the go command reads it.
func lookupEnv(environ []string, name string) (string, bool) {
	var (
		value string
		found bool
	)

	for _, entry := range environ {
		key, val, ok := strings.Cut(entry, "=")
		if ok && key == name {
			value, found = val, true
		}
	}

	return value, found
}
