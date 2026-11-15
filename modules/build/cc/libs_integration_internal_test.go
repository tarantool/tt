//go:build integration

package cc

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build/buildtest"
)

// requireLinux skips the calling test on a host that is not Linux: what it
// checks is read from ELF files.
func requireLinux(t *testing.T) {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skipf("the test reads ELF files, and %s builds none", runtime.GOOS)
	}
}

// runPaths returns the directories the dynamic section of the ELF file at
// path has the loader look in, DT_RUNPATH and DT_RPATH, in that order.
func runPaths(t *testing.T, path string) []string {
	t.Helper()

	file, err := elf.Open(path)
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	var dirs []string

	for _, tag := range []elf.DynTag{elf.DT_RUNPATH, elf.DT_RPATH} {
		values, err := file.DynString(tag)
		require.NoError(t, err)

		for _, value := range values {
			dirs = append(dirs, strings.Split(value, ":")...)
		}
	}

	return dirs
}

// neededLibraries returns DT_NEEDED of the ELF file at path.
func neededLibraries(t *testing.T, path string) []string {
	t.Helper()

	file, err := elf.Open(path)
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	needed, err := file.ImportedLibraries()
	require.NoError(t, err)

	return needed
}

// sharedLibrary builds the shared library lib<name>.so of the C source body,
// of the SONAME soname when it is not empty, into dir, with cc of PATH, and
// returns its path.
func sharedLibrary(t *testing.T, dir, name, soname, body string) string {
	t.Helper()

	require.NoError(t, os.MkdirAll(dir, 0o755))

	source := filepath.Join(dir, name+".c")
	require.NoError(t, os.WriteFile(source, []byte(body), 0o600))

	out := filepath.Join(dir, "lib"+name+".so")
	args := []string{"-shared", "-fPIC", "-o", out, source}

	if soname != "" {
		args = append(args, "-Wl,-soname,"+soname)
	}

	printed, err := exec.CommandContext(t.Context(), "cc", args...).CombinedOutput()
	require.NoError(t, err, string(printed))

	return out
}

// staticLibrary builds the static library lib<name>.a of the C source body
// into dir, compiled with cc of PATH and flags.
func staticLibrary(t *testing.T, dir, name, body string, flags ...string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(dir, 0o755))

	source := filepath.Join(dir, name+".c")
	object := filepath.Join(dir, name+".o")

	require.NoError(t, os.WriteFile(source, []byte(body), 0o600))

	args := append(append([]string{}, flags...), "-c", source, "-o", object)

	printed, err := exec.CommandContext(t.Context(), "cc", args...).CombinedOutput()
	require.NoError(t, err, string(printed))

	printed, err = exec.CommandContext(t.Context(), "ar", "rcs",
		filepath.Join(dir, "lib"+name+".a"), object).CombinedOutput()
	require.NoError(t, err, string(printed))
}

// fooC is the source of the library foo the module of answerC links.
const fooC = "int foo_answer(void) { return 42; }\n"

// answerC is a lua-c module answer whose function returns what foo_answer of
// the library foo returns.
const answerC = `#include <lua.h>

int foo_answer(void);

static int l_answer(lua_State *L) {
	lua_pushinteger(L, foo_answer());
	return 1;
}

int luaopen_answer(lua_State *L) {
	lua_pushcfunction(L, l_answer);
	return 1;
}
`

// answerCheck is a script that requires the module answer from the
// directory of its first argument and prints what its function returns.
const answerCheck = "package.cpath = arg[1] .. '/?.so;' .. arg[1] .. '/?.dylib;' .. " +
	"package.cpath\nprint(require('answer')())\nos.exit(0)\n"

// TestRpath checks with the compiler of the host that library_dirs adds no
// rpath to the module and rpath adds its entries, $ORIGIN as it is.
func TestRpath(t *testing.T) {
	t.Parallel()

	requireLinux(t)

	includeDir, _ := requireHostHeaders(t)

	harness := hostHarness(t, includeDir, map[string]string{"answer.c": answerC})

	sharedLibrary(t, filepath.Join(harness.Context.Dirs.Cwd, "vendor"), "foo", "", fooC)

	rest := map[string]any{
		"module": "answer", "sources": []any{"answer.c"},
		"library_dirs": []any{"vendor"}, "libraries": []any{"foo"},
	}

	hostBuild(t, harness, LuaC(), rest, "answer")

	module := filepath.Join(harness.Context.Dirs.Modules, "answer.so")
	assert.Empty(t, runPaths(t, module), "library_dirs adds no rpath")

	rest["rpath"] = []any{"${ORIGIN}/../vendor", "/opt/foo/lib"}

	bld, err := buildtest.Validate(LuaC(), flatComponent(), block(rest, nil))
	require.NoError(t, err)
	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())

	assert.Equal(t, []string{"$ORIGIN/../vendor", "/opt/foo/lib"}, runPaths(t, module))
}

// zlibC is a lua-c module zver whose function returns the version of zlib
// it is linked with, which a source compiles only with the headers of zlib.
const zlibC = `#include <lua.h>
#include <zlib.h>

static int l_version(lua_State *L) {
	lua_pushstring(L, zlibVersion());
	return 1;
}

int luaopen_zver(lua_State *L) {
	lua_pushcfunction(L, l_version);
	return 1;
}
`

// requirePkgConfig skips the calling test when pkg-config of PATH does not
// know the package pkg.
func requirePkgConfig(t *testing.T, pkg string) {
	t.Helper()

	err := exec.CommandContext(t.Context(), "pkg-config", "--exists", pkg).Run()
	if err != nil {
		t.Skipf("pkg-config knows no %s: %v", pkg, err)
	}
}

// TestPkgConfigWithZlib builds a lua-c module of zlib with pkg_config and
// the pkg-config of the host, and loads it in the host's Tarantool.
func TestPkgConfigWithZlib(t *testing.T) {
	t.Parallel()

	requirePkgConfig(t, "zlib")

	includeDir, tarantool := requireHostHeaders(t)

	harness := hostHarness(t, includeDir, map[string]string{
		"zver.c": zlibC,
		"check.lua": "package.cpath = arg[1] .. '/?.so;' .. arg[1] .. '/?.dylib;' .. " +
			"package.cpath\nprint(require('zver')())\nos.exit(0)\n",
	})

	hostBuild(t, harness, LuaC(), map[string]any{
		"module": "zver", "sources": []any{"zver.c"}, "pkg_config": []any{"zlib >= 1.2"},
	}, "zver")

	version, err := exec.CommandContext(t.Context(), "pkg-config", "--modversion",
		"zlib").Output()
	require.NoError(t, err)

	printed, err := exec.CommandContext(t.Context(), tarantool,
		filepath.Join(harness.Context.Dirs.Cwd, "check.lua"),
		harness.Context.Dirs.Modules).CombinedOutput()
	require.NoError(t, err, string(printed))
	assert.Equal(t, strings.TrimSpace(string(version)), strings.TrimSpace(string(printed)))

	if runtime.GOOS != "linux" {
		return
	}

	// With static = true the module takes libz.a, which the compiler
	// finds: one built with -fPIC is linked in, and the link of one that is
	// not, as that of Debian and Ubuntu, fails with the hint.
	harness.Context.Static = true

	bld, err := buildtest.Validate(LuaC(), flatComponent(), block(map[string]any{
		"module": "zver", "sources": []any{"zver.c"}, "pkg_config": []any{"zlib"},
	}, nil))
	require.NoError(t, err)

	err = harness.Run(t.Context(), bld)
	if err != nil {
		assert.Regexp(t, `hint: the static library /\S+/libz\.a was not built with -fPIC`,
			err.Error())

		return
	}

	for _, library := range neededLibraries(t, filepath.Join(harness.Context.Dirs.Modules,
		"zver.so")) {
		assert.NotContains(t, library, "libz.so")
	}
}

// fooNoPIC is a source of the library foo whose code, compiled without
// -fPIC, takes the address of its data absolutely, which a shared object
// cannot hold.
const fooNoPIC = "int foo_value = 42;\nint foo_answer(void) { return foo_value; }\n"

// TestStaticLibraries builds the module of answerC against the library foo
// of vendor/, with its static and shared libraries both there: without
// static the linker takes libfoo.so, which the module then needs; with
// static = true the build takes libfoo.a, with cc and with zig cc, and the
// module needs no libfoo and loads in the host's Tarantool; a file of
// libraries is linked as it is; and a static library built without -fPIC
// fails the link with the hint naming it.
func TestStaticLibraries(t *testing.T) {
	t.Parallel()

	requireLinux(t)

	includeDir, tarantool := requireHostHeaders(t)

	newHarness := func() *buildtest.Harness {
		harness := hostHarness(t, includeDir, map[string]string{
			"answer.c": answerC, "check.lua": answerCheck,
		})
		vendor := filepath.Join(harness.Context.Dirs.Cwd, "vendor")

		sharedLibrary(t, vendor, "foo", "libfoo.so.1", fooC)
		staticLibrary(t, vendor, "foo", fooC, "-fPIC")
		staticLibrary(t, vendor, "np", fooNoPIC, "-fno-pic", "-fno-pie")

		return harness
	}

	build := func(harness *buildtest.Harness, rest map[string]any, compiler string) error {
		validated, err := buildtest.Validate(LuaC(), flatComponent(), block(rest, nil))
		require.NoError(t, err)

		bld, ok := validated.(ccBuild)
		require.True(t, ok)

		bld.lookupEnv = func(key string) (string, bool) {
			if key != "CC" || compiler == "" {
				return "", false
			}

			return compiler, true
		}

		return harness.Run(t.Context(), bld)
	}

	load := func(harness *buildtest.Harness) {
		printed, err := exec.CommandContext(t.Context(), tarantool,
			filepath.Join(harness.Context.Dirs.Cwd, "check.lua"),
			harness.Context.Dirs.Modules).CombinedOutput()
		require.NoError(t, err, string(printed))
		assert.Equal(t, "42", strings.TrimSpace(string(printed)))
	}

	byName := map[string]any{
		"module": "answer", "sources": []any{"answer.c"},
		"library_dirs": []any{"vendor"}, "libraries": []any{"foo"},
	}

	harness := newHarness()
	module := filepath.Join(harness.Context.Dirs.Modules, "answer.so")

	require.NoError(t, build(harness, byName, ""), harness.Printed())
	assert.Contains(t, neededLibraries(t, module), "libfoo.so.1", "the shared library")

	compilers := []string{""}

	zig, err := exec.LookPath("zig")
	if err == nil {
		compilers = append(compilers, zig+" cc")
	}

	for _, compiler := range compilers {
		harness := newHarness()

		harness.Context.Static = true

		require.NoError(t, build(harness, byName, compiler), "CC=%q", compiler)
		assert.NotContains(t, neededLibraries(t, filepath.Join(harness.Context.Dirs.Modules,
			"answer.so")), "libfoo.so.1", "CC=%q", compiler)
		load(harness)
	}

	byFile := map[string]any{
		"module": "answer", "sources": []any{"answer.c"}, "libraries": []any{"vendor/libfoo.a"},
	}

	harness = newHarness()

	require.NoError(t, build(harness, byFile, ""))
	assert.NotContains(t, neededLibraries(t, filepath.Join(harness.Context.Dirs.Modules,
		"answer.so")), "libfoo.so.1")
	load(harness)

	notPIC := map[string]any{
		"module": "answer", "sources": []any{"answer.c"}, "libraries": []any{"vendor/libnp.a"},
	}

	for _, compiler := range compilers {
		err := build(newHarness(), notPIC, compiler)
		require.Error(t, err, "CC=%q", compiler)
		assert.Contains(t, err.Error(), "hint: the static library vendor/libnp.a was not "+
			"built with -fPIC", "CC=%q", compiler)
	}
}

// barC is the source of a library bar the library foo of fooOfBarC needs.
const barC = "int bar_answer(void) { return 42; }\n"

// fooOfBarC is a source of the library foo that needs the library bar.
const fooOfBarC = "int bar_answer(void);\nint foo_answer(void) { return bar_answer(); }\n"

// TestBundle builds the module of answerC against the shared library foo of
// vendor/, of the SONAME libfoo.so.1, and bundles it: the build copies it
// beside the module as libfoo.so.1, the module asks for it there by
// $ORIGIN, and loads in Tarantool from another directory, with vendor/ gone
// and no LD_LIBRARY_PATH; with static = true as well, which links the
// shared library though the static one is there. A library the bundled one
// needs and bundle does not list is warned of, and a library the module
// does not link is refused.
func TestBundle(t *testing.T) {
	t.Parallel()

	requireLinux(t)

	includeDir, tarantool := requireHostHeaders(t)

	rest := map[string]any{
		"module": "answer", "sources": []any{"answer.c"}, "library_dirs": []any{"vendor"},
		"libraries": []any{"foo"}, "bundle": []any{"foo"},
	}

	for _, static := range []bool{false, true} {
		harness := hostHarness(t, includeDir, map[string]string{
			"answer.c": answerC, "check.lua": answerCheck,
		})
		vendor := filepath.Join(harness.Context.Dirs.Cwd, "vendor")
		library := sharedLibrary(t, vendor, "foo", "libfoo.so.1", fooC)

		require.NoError(t, os.Rename(library, library+".1.0"))
		require.NoError(t, os.Symlink("libfoo.so.1.0", library))
		staticLibrary(t, vendor, "foo", fooC, "-fPIC")

		harness.Context.Static = static
		// The module is loaded once it is linked, and finds the copy beside
		// it already.
		harness.Context.Tarantool.Executable = tarantool

		hostBuild(t, harness, LuaC(), rest, "answer")

		modules := harness.Context.Dirs.Modules
		module := filepath.Join(modules, "answer.so")
		bundled := filepath.Join(modules, "libfoo.so.1")

		assert.FileExists(t, bundled, "the copy is named by the SONAME")
		assert.Contains(t, neededLibraries(t, module), "libfoo.so.1")
		assert.Equal(t, []string{"$ORIGIN"}, runPaths(t, module))
		assert.Empty(t, harness.Warnings())

		// Installed elsewhere, with what it was built of gone.
		installed := t.TempDir()

		for _, name := range []string{"answer.so", "libfoo.so.1"} {
			data, err := os.ReadFile(filepath.Join(modules, name))
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(installed, name), data, 0o755))
		}

		require.NoError(t, os.RemoveAll(vendor))

		cmd := exec.CommandContext(t.Context(), tarantool,
			filepath.Join(harness.Context.Dirs.Cwd, "check.lua"), installed)

		cmd.Env = withoutLibraryPath(os.Environ())

		printed, err := cmd.CombinedOutput()
		require.NoError(t, err, string(printed))
		assert.Equal(t, "42", strings.TrimSpace(string(printed)), "static = %t", static)
	}

	// foo needs bar, which bundle does not list.
	harness := hostHarness(t, includeDir, map[string]string{"answer.c": answerC})
	vendor := filepath.Join(harness.Context.Dirs.Cwd, "vendor")

	sharedLibrary(t, vendor, "bar", "libbar.so.1", barC)

	source := filepath.Join(vendor, "foo.c")

	require.NoError(t, os.WriteFile(source, []byte(fooOfBarC), 0o600))

	printed, err := exec.CommandContext(t.Context(), "cc", "-shared", "-fPIC", "-o",
		filepath.Join(vendor, "libfoo.so"), source, "-Wl,-soname,libfoo.so.1", "-L"+vendor,
		"-lbar").CombinedOutput()
	require.NoError(t, err, string(printed))

	hostBuild(t, harness, LuaC(), rest, "answer")
	assert.Equal(t, []string{filepath.Join(harness.Context.Dirs.Modules, "libfoo.so.1") +
		", which bundle copies beside the module, needs libbar.so.1, which is not bundled: " +
		"the machine the module is installed on is to have it, or bundle lists it too"},
		harness.Warnings())

	rest["bundle"] = []any{"bar"}

	bld, err := buildtest.Validate(LuaC(), flatComponent(), block(rest, nil))
	require.NoError(t, err)

	err = harness.Run(t.Context(), bld)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `bundle[0]: "bar" is no library the module links: `+
		"bundle names one of libraries, of link or of pkg_config")
}

// withoutLibraryPath is environ without LD_LIBRARY_PATH and
// DYLD_LIBRARY_PATH.
func withoutLibraryPath(environ []string) []string {
	var kept []string

	for _, entry := range environ {
		if !strings.HasPrefix(entry, "LD_LIBRARY_PATH=") &&
			!strings.HasPrefix(entry, "DYLD_LIBRARY_PATH=") {
			kept = append(kept, entry)
		}
	}

	return kept
}
