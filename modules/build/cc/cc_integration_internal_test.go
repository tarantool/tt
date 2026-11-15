//go:build integration

package cc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// hostHarness is a harness of a module component built against the host's
// Tarantool, whose headers are includeDir, <prefix>/include/tarantool, with
// the sources of sources in its working directory. It is a harness of c,
// whose builds are those of lua-c, both making modules.
func hostHarness(t *testing.T, includeDir string, sources map[string]string) *buildtest.Harness {
	t.Helper()

	harness := buildtest.New(t, C(), component())

	harness.SetTarantool(build.Tarantool{
		Prefix: filepath.Dir(filepath.Dir(includeDir)), IncludeDir: includeDir, Version: "",
	})

	for name, body := range sources {
		require.NoError(t, os.WriteFile(filepath.Join(harness.Context.Dirs.Cwd, name),
			[]byte(body), 0o600))
	}

	return harness
}

// hostBuild runs the build of the block rest of backend in harness and
// checks that it made the module module.
func hostBuild(
	t *testing.T, harness *buildtest.Harness, backend build.Backend, rest map[string]any,
	module string,
) {
	t.Helper()

	bld, err := buildtest.Validate(backend, component(), block(rest, nil))
	require.NoError(t, err)

	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())
	assert.FileExists(t, filepath.Join(harness.Context.Dirs.Modules,
		module+harness.Context.Target.ModuleExt()))
}

func TestCcCompilesSingleSource(t *testing.T) {
	t.Parallel()

	includeDir, _ := requireHostHeaders(t)

	harness := hostHarness(t, includeDir, map[string]string{
		"answer.c": "int answer(void) { return 42; }\n",
	})

	hostBuild(t, harness, C(),
		map[string]any{"module": "answer", "sources": []any{"answer.c"}}, "answer")
}

func TestCcCompilesMultiSource(t *testing.T) {
	t.Parallel()

	includeDir, _ := requireHostHeaders(t)

	// part_b references a from part_a, so a passing link proves both objects
	// end up in one module; the prototype avoids an implicit-declaration
	// error.
	harness := hostHarness(t, includeDir, map[string]string{
		"part_a.c": "int a(void) { return 1; }\n",
		"part_b.c": "int a(void);\nint b(void) { return a() + 1; }\n",
	})

	hostBuild(t, harness, LuaC(),
		map[string]any{"module": "parts", "sources": []any{"part_a.c", "part_b.c"}}, "parts")
}

// TestCcTakesTheProjectFlagsThenTheUsers proves the order of the flags with
// a real compiler: the project's -Werror fails a source with a warning, and
// -Wno-error of CFLAGS of the environment, which comes after it, builds it.
// t.Setenv keeps the test out of parallel runs.
func TestCcTakesTheProjectFlagsThenTheUsers(t *testing.T) {
	includeDir, _ := requireHostHeaders(t)

	sources := map[string]string{"warn.c": "int warn(void) { int unused; return 0; }\n"}
	rest := map[string]any{
		"module": "warn", "sources": []any{"warn.c"}, "cflags": []any{"-Wall", "-Werror"},
	}

	bld, err := buildtest.Validate(C(), component(), block(rest, nil))
	require.NoError(t, err)

	t.Setenv("CFLAGS", "")

	failed := hostHarness(t, includeDir, sources)
	require.Error(t, failed.Run(t.Context(), bld), "-Werror of cflags fails the build")

	t.Setenv("CFLAGS", "-O0 -Wno-error")

	hostBuild(t, hostHarness(t, includeDir, sources), C(), rest, "warn")
}

// TestCcCompilesAgainstTarantoolHeaders proves the include-dir wiring works
// against the real Tarantool C module API: the source includes <module.h> and
// exports a luaopen_ entry point, so it compiles only when the include
// directory of the toolchain actually resolves the headers
// requireHostHeaders found.
func TestCcCompilesAgainstTarantoolHeaders(t *testing.T) {
	t.Parallel()

	includeDir, _ := requireHostHeaders(t)

	harness := hostHarness(t, includeDir, map[string]string{
		"headercheck.c": "#include <module.h>\n" +
			"int luaopen_headercheck(lua_State *L) { (void)L; return 0; }\n",
	})

	hostBuild(t, harness, LuaC(),
		map[string]any{"module": "headercheck", "sources": []any{"headercheck.c"}},
		"headercheck")
}

// fastHashC is a minimal Lua C extension exporting luaopen_fast_hash.
const fastHashC = `#include <lua.h>

static int l_hash(lua_State *L) {
	lua_pushinteger(L, 42);
	return 1;
}

int luaopen_fast_hash(lua_State *L) {
	lua_pushcfunction(L, l_hash);
	return 1;
}
`

// TestLuaCModuleLoadsInTarantool builds a Lua C module with lua-c, static
// and not, against the headers of the host's Tarantool (see
// requireHostHeaders), and loads it in that Tarantool.
func TestLuaCModuleLoadsInTarantool(t *testing.T) {
	t.Parallel()

	includeDir, tarantool := requireHostHeaders(t)

	for _, static := range []bool{false, true} {
		harness := hostHarness(t, includeDir, map[string]string{
			"fast_hash.c": fastHashC,
			"check.lua": "package.cpath = arg[1] .. '/?.so;' .. arg[1] .. '/?.dylib;' .. " +
				"package.cpath\nprint(require('fast_hash')())\nos.exit(0)\n",
		})

		harness.Context.Static = static

		hostBuild(t, harness, LuaC(),
			map[string]any{"module": "fast_hash", "sources": []any{"fast_hash.c"}}, "fast_hash")

		printed, err := exec.CommandContext(t.Context(), tarantool,
			filepath.Join(harness.Context.Dirs.Cwd, "check.lua"),
			harness.Context.Dirs.Modules).CombinedOutput()
		require.NoError(t, err, string(printed))
		assert.Equal(t, "42", strings.TrimSpace(string(printed)), "static = %t", static)
	}
}
