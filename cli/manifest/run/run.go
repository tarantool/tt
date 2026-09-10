// Package run answers the two questions every workflow shortcut over a tt
// package has to answer before it can hand control to Tarantool: which
// interpreter this project runs under, and how the tt process gets out of the
// way once it is chosen.
//
// A project is a directory holding app.manifest.toml, and nothing else is
// required of it — no tt environment, no tt.yaml. The interpreter is the one an
// installed package bundles under _runtime/ when there is one, and the host's
// otherwise; TT_USE_SYSTEM_TARANTOOL inverts that. Handing over is a real
// exec: the process image is replaced, so signals, the terminal and the exit
// code pass through by construction rather than by being forwarded.
//
// tt run is this package plus argument passing. tt test is this package plus a
// script to feed the interpreter, which is why locating luatest's entry point
// lives here too.
package run

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// Names the project layout fixes.
const (
	// ManifestFileName is the file whose presence makes a directory a project
	// root.
	ManifestFileName = "app.manifest.toml"
	// TarantoolName is the interpreter's executable name, both in _runtime/ and
	// on PATH.
	TarantoolName = "tarantool"
	// UseSystemEnv forces the host interpreter, ignoring a bundled _runtime/.
	UseSystemEnv = "TT_USE_SYSTEM_TARANTOOL"

	// runtimeDirName is the bundled-runtime tree a with-deps install lays down
	// beside the manifest.
	runtimeDirName = "_runtime"
	// rocksDirName is the project-scope rocks tree.
	rocksDirName = ".rocks"
	// binDirName holds the console scripts rocks install, and the bin/ level of
	// a bundled runtime.
	binDirName = "bin"
	// rocksInstallDir is where LuaRocks keeps per-rock metadata and the
	// unwrapped copy of every console script it deployed.
	rocksInstallDir = "share/tarantool/rocks"
	// luatestName is the rock tt test drives.
	luatestName = "luatest"
)

var (
	// ErrNoManifest reports a directory that is not a package root.
	ErrNoManifest = errors.New("not a tt package directory")
	// ErrNoTarantool reports that neither the bundled nor the host interpreter
	// could be found.
	ErrNoTarantool = errors.New("no tarantool executable found")
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

// Environment is what the tt process already knows about its host, passed in
// rather than read here so selection is a pure function of its inputs.
type Environment struct {
	// Executable is the tarantool the tt environment resolved (bin_dir first,
	// then PATH). Empty means it resolved none, and PATH is searched directly.
	Executable string
	// UseSystem forces the host interpreter, ignoring a bundled _runtime/.
	UseSystem bool
}

// UseSystemFromEnv reads UseSystem from the process environment. Anything but
// an unset, empty, "0" or "false" value enables it, so the documented "=1"
// works and so does every other way of plainly saying yes.
func UseSystemFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(UseSystemEnv))) {
	case "", "0", "false":
		return false
	default:
		return true
	}
}

// SelectTarantool picks the interpreter root runs under: the bundled one first,
// the host's second.
//
// The bundled interpreter wins because a with-deps package was installed
// precisely so it would carry its own runtime; running it under whatever the
// host happens to have is the case that package exists to avoid. UseSystem
// inverts the order for the developer who wants their own build against an
// installed tree, and it is the only way to get there — there is no flag,
// because every argument belongs to Tarantool.
//
// Both places are named in the error, since which of the two was missing is the
// whole content of the answer.
func SelectTarantool(root string, env Environment) (string, error) {
	bundled := bundledTarantool(root)

	if !env.UseSystem && bundled != "" {
		return bundled, nil
	}

	if env.Executable != "" {
		return env.Executable, nil
	}

	found, lookErr := exec.LookPath(TarantoolName)
	if lookErr == nil {
		return found, nil
	}

	return "", fmt.Errorf("%w: no %s under %s and none on PATH",
		ErrNoTarantool, TarantoolName, filepath.Join(root, runtimeDirName))
}

// bundledTarantool returns the executable of root's bundled runtime, or an
// empty string when there is none.
//
// Two layouts are accepted because two produce a bundle. A runtime taken from
// the cache is copied wholesale and keeps its own <prefix>/bin/tarantool; the
// single-binary fallback writes the same path. An Enterprise SDK tree is flat —
// the interpreter sits at the top of the prefix with no bin/ level — so a cache
// entry populated from one lands the binary one directory higher.
func bundledTarantool(root string) string {
	dir := filepath.Join(root, runtimeDirName, TarantoolName)

	candidates := []string{
		filepath.Join(dir, binDirName, TarantoolName),
		filepath.Join(dir, TarantoolName),
	}

	for _, candidate := range candidates {
		if isExecutableFile(candidate) {
			return candidate
		}
	}

	return ""
}

// isExecutableFile reports whether path is a regular file anyone may execute.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}

	return info.Mode().Perm()&0o111 != 0
}

// Exec replaces the tt process with tarantool, running in root with argv
// appended verbatim.
//
// The host environment is inherited unfiltered and no LUA_PATH or LUA_CPATH is
// composed: Tarantool finds .rocks/ from its own working directory, so moving
// there is the whole of what tt has to arrange. It returns only when the
// replacement itself fails.
func Exec(root, tarantool string, argv []string) error {
	chdirErr := os.Chdir(root)
	if chdirErr != nil {
		return fmt.Errorf("entering %s: %w", root, chdirErr)
	}

	// argv[0] is the interpreter itself, as an exec'd process expects.
	args := append([]string{tarantool}, argv...)

	// The interpreter comes from the project's own tree or the host's PATH, and
	// becoming it is the whole operation rather than a side effect of one.
	//nolint:gosec // G204: execing the selected interpreter is the point.
	execErr := syscall.Exec(tarantool, args, os.Environ())
	if execErr != nil {
		return fmt.Errorf("running %s: %w", tarantool, execErr)
	}

	// Unreachable: a successful exec never returns to this process.
	return nil
}

// LuatestScript locates the luatest entry script in root's rocks tree, so it
// can be fed to the interpreter SelectTarantool chose.
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
