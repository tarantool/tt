// Package run answers the two questions every workflow shortcut over a tt
// package has to answer before it can hand control to Tarantool: which
// interpreter this project runs under, and how the tt process gets out of the
// way once it is chosen.
//
// A project is a directory holding app.manifest.toml, and nothing else is
// required of it — no tt environment, no tt.yaml. The interpreter is the one
// cli/tarantool chooses for a project, the rule tt instance follows too: the
// tarantool .tarantool/bin/tarantool selects, which tt binary sets and a
// with-deps tt package install sets to the bundled runtime when the project
// has none, and otherwise the first on PATH; TT_USE_SYSTEM_TARANTOOL forces
// PATH. An installed version of an application in the user or system scope is
// no project: it runs on the tarantool its lock names in that scope. Handing
// over is a real exec: the process image is replaced, so
// signals, the terminal and the exit code pass through by construction rather
// than by being forwarded.
//
// tt run is this package plus argument passing. tt test is this package plus a
// script to feed the interpreter, which is why locating luatest's entry point
// lives here too.
package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/scope"
)

// Names the project layout fixes.
const (
	// ManifestFileName is the file whose presence makes a directory a project
	// root.
	ManifestFileName = "app.manifest.toml"

	// rocksDirName is the project-scope rocks tree.
	rocksDirName = ".rocks"
	// binDirName holds the console scripts rocks install.
	binDirName = "bin"
	// rocksInstallDir is where LuaRocks keeps per-rock metadata and the
	// unwrapped copy of every console script it deployed.
	rocksInstallDir = "share/tarantool/rocks"
	// luatestName is the rock tt test drives.
	luatestName = "luatest"
)

// TestDirNames are the directories a test run looks for, in order. Both spell
// the same convention and projects in the wild use both.
var TestDirNames = []string{"test", "tests"}

var (
	// ErrNoManifest reports a directory that is not a package root.
	ErrNoManifest = errors.New("not a tt package directory")
	// ErrNoTestDir reports a project with nowhere to run tests from.
	ErrNoTestDir = errors.New("no test directory")
	// ErrNoTarantool reports that the project has no tarantool of its own and
	// there is none on PATH. It is the resolver's error, so errors.Is matches
	// it whichever package a caller names it by.
	ErrNoTarantool = tarantool.ErrNoTarantool
	// ErrNoLuatest reports that the project's rocks tree holds no luatest.
	ErrNoLuatest = errors.New("luatest is not installed in the project")
)

// ProjectRoot reports dir as a project root, failing when it holds no manifest.
//
// The root is the caller's working directory and is never searched for upwards:
// which directory a command acts on is then exactly the one the shell is in,
// with no rule to remember and no way for a stray manifest in an ancestor to
// capture a run.
func ProjectRoot(dir string) (string, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", dir, err)
	}

	info, err := os.Stat(filepath.Join(root, ManifestFileName))
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("%w: no %s in %s", ErrNoManifest, ManifestFileName, root)
	}

	return root, nil
}

// Tarantool chooses the interpreter the project at root runs under. It is
// cli/tarantool's choice for the project scope, so tt run, tt test and the
// instance commands start the same tarantool: the one
// root/.tarantool/bin/tarantool selects, else the first on PATH, with
// TT_USE_SYSTEM_TARANTOOL forcing PATH.
//
// The root is a project directory found as ProjectRoot finds it, not by the
// upward search the resolver's own callers do; the link is looked up in that
// very directory. A with-deps tt package install into a project with no
// tarantool of its own selects the bundled one there, so a package installed
// that way runs on the runtime it was packed with.
//
// A root inside an installed version, <root>/versions/<app>/<app>_<ver> or the
// <root>/apps/<app> link to it, is not a project: it runs on the tarantool its
// lock names in its own scope, as the instance commands start it, rather than
// on whatever PATH offers. The version is read through the link once, here,
// so the choice is the version the link names at this moment.
//
// The warnings the choice comes with, such as a project tarantool that is not
// the one the lock bundles, are in the result, for the caller to report.
func Tarantool(root string) (tarantool.Executable, error) {
	app, err := tarantoolApp(root)
	if err != nil {
		return tarantool.Executable{}, err
	}

	exe, err := tarantool.Resolve(app)
	if err != nil {
		return tarantool.Executable{}, fmt.Errorf("choosing the tarantool of %s: %w", root, err)
	}

	return exe, nil
}

// tarantoolApp describes the application at root to the resolver: a project,
// or the installed version root is in, in the scope that installed it.
func tarantoolApp(root string) (tarantool.App, error) {
	installation, found, err := scope.FindInstallation(root)
	if err != nil {
		return tarantool.App{}, fmt.Errorf("choosing the tarantool of %s: %w", root, err)
	}

	if !found {
		return tarantool.App{Scope: binary.ScopeProject, Dir: root}, nil
	}

	version, err := filepath.EvalSymlinks(root)
	if err != nil {
		return tarantool.App{}, fmt.Errorf("choosing the tarantool of %s: %w", root, err)
	}

	appScope := binary.ScopeSystem
	if installation.User {
		appScope = binary.ScopeUser
	}

	return tarantool.App{Scope: appScope, Dir: version}, nil
}

// isExecutableFile reports whether path is a regular file anyone may execute.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}

	return info.Mode().Perm()&0o111 != 0
}

// Exec replaces the tt process with the tarantool at executable, running in
// root with argv appended verbatim.
//
// The host environment is inherited unfiltered and no LUA_PATH or LUA_CPATH is
// composed: Tarantool finds .rocks/ from its own working directory, so moving
// there is the whole of what tt has to arrange. It returns only when the
// replacement itself fails.
func Exec(root, executable string, argv []string) error {
	chdirErr := os.Chdir(root)
	if chdirErr != nil {
		return fmt.Errorf("entering %s: %w", root, chdirErr)
	}

	// argv[0] is the interpreter itself, as an exec'd process expects.
	args := append([]string{executable}, argv...)

	// The interpreter comes from the project's own tree or the host's PATH, and
	// becoming it is the whole operation rather than a side effect of one.
	execErr := syscall.Exec(executable, args, os.Environ())
	if execErr != nil {
		return fmt.Errorf("running %s: %w", executable, execErr)
	}

	// Unreachable: a successful exec never returns to this process.
	return nil
}

// TestDir resolves what a test run is pointed at, as a path relative to root.
//
// subPath narrows the run and is itself relative to the project root; it may
// name a directory or a single test file, and it must exist — a typo that
// silently ran the whole suite, or nothing at all, is worse than an error. With
// no subPath the conventional directories are tried in order.
//
// The result stays relative so it reads in the runner's own output the way the
// developer typed it, and because the run happens in root anyway. A directory
// comes back with a trailing separator, which luatest requires: without one it
// reads the argument as a test group name and refuses it as invalid.
func TestDir(root, subPath string) (string, error) {
	if subPath != "" {
		return checkedSubPath(root, subPath)
	}

	for _, name := range TestDirNames {
		info, err := os.Stat(filepath.Join(root, name))
		if err == nil && info.IsDir() {
			return name + string(filepath.Separator), nil
		}
	}

	wanted := make([]string, 0, len(TestDirNames))
	for _, name := range TestDirNames {
		wanted = append(wanted, name+"/")
	}

	return "", fmt.Errorf("%w: no %s directory in %s",
		ErrNoTestDir, strings.Join(wanted, " or "), root)
}

// checkedSubPath validates a caller-supplied test path against root.
func checkedSubPath(root, subPath string) (string, error) {
	clean := filepath.Clean(subPath)
	if filepath.IsAbs(clean) {
		relative, err := filepath.Rel(root, clean)
		if err != nil {
			return "", fmt.Errorf("%w: %s is outside %s", ErrNoTestDir, subPath, root)
		}

		clean = relative
	}

	// A path that climbs out of the project would run tests from somewhere the
	// project's own .rocks/ tree does not describe.
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s is outside %s", ErrNoTestDir, subPath, root)
	}

	info, err := os.Stat(filepath.Join(root, clean))
	if err != nil {
		return "", fmt.Errorf("%w: %s does not exist in %s", ErrNoTestDir, subPath, root)
	}

	if info.IsDir() {
		return clean + string(filepath.Separator), nil
	}

	return clean, nil
}

// LuatestScript locates the luatest entry script in root's rocks tree, so it
// can be fed to the interpreter Tarantool chose.
//
// The per-rock copy under share/tarantool/rocks/ is preferred over .rocks/bin/
// because LuaRocks may deploy a console script either way: verbatim, or as a
// generated /bin/sh launcher that execs an interpreter path baked in at install
// time. Running the launcher would silently ignore the interpreter this package
// just chose, so the script LuaRocks kept beside the rock is what tt runs.
func LuatestScript(root string) (string, error) {
	rocks := filepath.Join(root, rocksDirName)

	pattern := filepath.Join(rocks, filepath.FromSlash(rocksInstallDir),
		luatestName, "*", binDirName, luatestName)

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("looking for %s: %w", luatestName, err)
	}

	if len(matches) > 0 {
		// Several installed versions are legal in a tree LuaRocks manages; the
		// highest directory name is the one .rocks/bin/ points at.
		sort.Sort(sort.Reverse(sort.StringSlice(matches)))

		return matches[0], nil
	}

	deployed := filepath.Join(rocks, binDirName, luatestName)
	if isExecutableFile(deployed) {
		return deployed, nil
	}

	return "", fmt.Errorf("%w: no %s under %s", ErrNoLuatest, luatestName, rocks)
}

// LuatestInstalled reports whether root's rocks tree already holds luatest, so
// a caller can skip resolving and fetching one it does not need.
func LuatestInstalled(root string) bool {
	_, err := LuatestScript(root)

	return err == nil
}
