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
// into dir, compiled with cc of PATH and flags, and returns its path.
func staticLibrary(t *testing.T, dir, name, body string, flags ...string) string {
	t.Helper()

	require.NoError(t, os.MkdirAll(dir, 0o755))

	source := filepath.Join(dir, name+".c")
	object := filepath.Join(dir, name+".o")
	require.NoError(t, os.WriteFile(source, []byte(body), 0o600))

	args := append(append([]string{}, flags...), "-c", source, "-o", object)

	printed, err := exec.CommandContext(t.Context(), "cc", args...).CombinedOutput()
	require.NoError(t, err, string(printed))

	out := filepath.Join(dir, "lib"+name+".a")

	printed, err = exec.CommandContext(t.Context(), "ar", "rcs", out, object).CombinedOutput()
	require.NoError(t, err, string(printed))

	return out
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
}
