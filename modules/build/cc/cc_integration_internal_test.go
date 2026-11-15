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

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// flatComponent is the component of the host tests: of no namespace, so
// that the module of the output directory is required by its name, as the
// tests load it.
func flatComponent() build.Component {
	comp := component()

	comp.Namespace = ""

	return comp
}

// hostHarness is a harness of a module component built against the host's
// Tarantool, whose headers are includeDir, <prefix>/include/tarantool, with
// the sources of sources in its working directory, which may be in
// directories of it. It is a harness of c, whose builds are those of lua-c,
// both making modules.
func hostHarness(t *testing.T, includeDir string, sources map[string]string) *buildtest.Harness {
	t.Helper()

	harness := buildtest.New(t, C(), flatComponent())

	harness.SetTarantool(build.Tarantool{
		Prefix: filepath.Dir(filepath.Dir(includeDir)), IncludeDir: includeDir, Version: "",
	})

	for name, body := range sources {
		writeFile(t, harness, name, body)
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

	bld, err := buildtest.Validate(backend, flatComponent(), block(rest, nil))
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

	bld, err := buildtest.Validate(C(), flatComponent(), block(rest, nil))
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

// requireCXX skips the calling test when the C++ compiler the build takes,
// $CXX or c++, is not in PATH.
func requireCXX(t *testing.T) {
	t.Helper()

	name := os.Getenv("CXX")
	if name == "" {
		name = "c++"
	}

	_, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("C++ compiler %q not found in PATH: %v", name, err)
	}
}

// mixedSources are the sources of a lua-c module of C and C++: the entry
// point in C, a function of C++ that uses std::vector and catches the
// exception it throws, and one of the Lua API in C++, through lua.hpp.
var mixedSources = map[string]string{
	"mixed.c": `#include <lua.h>
#include <lauxlib.h>

int vec_sum(int n);
int twice(lua_State *L);

static int l_sum(lua_State *L) {
	lua_pushinteger(L, vec_sum(luaL_checkint(L, 1)));
	return 1;
}

int luaopen_mixed(lua_State *L) {
	lua_newtable(L);
	lua_pushcfunction(L, l_sum);
	lua_setfield(L, -2, "sum");
	lua_pushcfunction(L, twice);
	lua_setfield(L, -2, "twice");
	return 1;
}
`,
	"cpp/vec.cpp": `#include <stdexcept>
#include <vector>

extern "C" int vec_sum(int n) {
	std::vector<int> v;
	for (int i = 1; i <= n; i++)
		v.push_back(i);
	try {
		if (n < 0)
			throw std::invalid_argument("negative");
		(void)v.at(n);
	} catch (const std::out_of_range &) {
		int sum = 0;
		for (int x : v)
			sum += x;
		return sum;
	} catch (const std::exception &) {
		return -1;
	}
	return 0;
}
`,
	"cpp/twice.cc": `#include <lua.hpp>

extern "C" int twice(lua_State *L) {
	lua_pushinteger(L, 2 * luaL_checkinteger(L, 1));
	return 1;
}
`,
	"check.lua": "package.cpath = arg[1] .. '/?.so;' .. arg[1] .. '/?.dylib;' .. package.cpath\n" +
		"local mixed = require('mixed')\n" +
		"print(mixed.sum(10), mixed.sum(-1), mixed.twice(21))\nos.exit(0)\n",
}

// mixedBlock is the build block of the module of mixedSources.
func mixedBlock() map[string]any {
	return map[string]any{
		"module":   "mixed",
		"sources":  []any{"mixed.c", "cpp/vec.cpp", "cpp/twice.cc"},
		"cflags":   []any{"-std=gnu99", "-Wall", "-Werror"},
		"cxxflags": []any{"-std=c++17", "-Wall", "-Werror"},
	}
}

// TestLuaCMixedModuleLoadsInTarantool builds a lua-c module of C and C++,
// static and not, and loads it in the host's Tarantool: the C++ code runs,
// its exceptions caught inside it. The static one needs no libstdc++ of
// the system on Linux.
func TestLuaCMixedModuleLoadsInTarantool(t *testing.T) {
	t.Parallel()

	includeDir, tarantool := requireHostHeaders(t)
	requireCXX(t)

	for _, static := range []bool{false, true} {
		harness := hostHarness(t, includeDir, mixedSources)

		harness.Context.Static = static

		hostBuild(t, harness, LuaC(), mixedBlock(), "mixed")

		printed, err := exec.CommandContext(t.Context(), tarantool,
			filepath.Join(harness.Context.Dirs.Cwd, "check.lua"),
			harness.Context.Dirs.Modules).CombinedOutput()
		require.NoError(t, err, string(printed))
		assert.Equal(t, "55\t-1\t42", strings.TrimSpace(string(printed)), "static = %t", static)

		if static && runtime.GOOS == "linux" {
			module, err := elf.Open(filepath.Join(harness.Context.Dirs.Modules, "mixed.so"))
			require.NoError(t, err)

			needed, err := module.ImportedLibraries()
			require.NoError(t, module.Close())
			require.NoError(t, err)

			for _, library := range needed {
				assert.NotContains(t, library, "libstdc++", "static = true")
			}
		}
	}
}

// wrapper is a compiler wrapper that logs each run, its arguments, to
// $WRAP_LOG and runs them: CC="wrap cc" counts the runs of cc.
const wrapper = `#!/bin/sh
args="$*"
printf '%s\n' "$args" >> "$WRAP_LOG"
exec "$@"
`

// wrapLog is the log of wrapper, and how much of it was read.
type wrapLog struct {
	path string
	read int
}

// wrappedBuild is the c build of rest in harness with CC and CXX of the
// environment of the test behind wrapper, which logs into log.path.
func wrappedBuild(t *testing.T, harness *buildtest.Harness, rest map[string]any,
	log *wrapLog,
) ccBuild {
	t.Helper()

	validated, err := buildtest.Validate(C(), flatComponent(), block(rest, nil))
	require.NoError(t, err)

	bld, ok := validated.(ccBuild)
	require.True(t, ok)

	// Written once: the wrapper written again is another compiler, its
	// time of change another.
	if _, set := harness.Context.Env["WRAP_LOG"]; !set {
		writeTool(t, harness, "wrap", wrapper)
	}

	harness.Context.Env["WRAP_LOG"] = log.path

	environ := map[string]string{"CC": "wrap cc", "CXX": "wrap c++"}

	for _, variable := range []string{"CC", "CXX"} {
		if value := os.Getenv(variable); value != "" {
			environ[variable] = "wrap " + value
		}
	}

	bld.lookupEnv = func(key string) (string, bool) {
		value, set := environ[key]

		return value, set
	}

	return bld
}

// next returns the sources the compiles logged since the last call compiled,
// sorted, and whether a link was logged; the probes of the compilers are
// left out.
func (l *wrapLog) next(t *testing.T) ([]string, bool) {
	t.Helper()

	data, err := os.ReadFile(l.path)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")

	var ran []string

	for _, line := range lines[l.read:] {
		if !strings.HasSuffix(line, " -dumpmachine") && !strings.HasSuffix(line, " --version") {
			ran = append(ran, line)
		}
	}

	l.read = len(lines)

	return compiled(ran)
}

// TestCacheWithTheCompiler checks the cache with the compilers of the host: a
// build with nothing changed runs no compile and no link, a header the
// depfile lists compiles the objects that include it, cflags compile the C
// objects, and a source the block drops has its object removed.
func TestCacheWithTheCompiler(t *testing.T) {
	t.Parallel()

	includeDir, _ := requireHostHeaders(t)
	requireCXX(t)

	harness := hostHarness(t, includeDir, map[string]string{
		"include/common.h": "#define COMMON 1\n",
		"a.c":              "#include \"common.h\"\nint a(void) { return COMMON; }\n",
		"b.c":              "#include <module.h>\nint b(void) { return 2; }\n",
		"c.cpp": "#include <vector>\n" +
			"extern \"C\" int c() { return std::vector<int>(3).size(); }\n",
	})

	rest := map[string]any{
		"module": "cached", "sources": []any{"a.c", "b.c", "c.cpp"},
		"include_dirs": []any{"include"},
	}

	log := &wrapLog{path: filepath.Join(t.TempDir(), "wrap.log"), read: 0}
	run := func(rest map[string]any) {
		t.Helper()

		require.NoError(t, harness.Run(t.Context(), wrappedBuild(t, harness, rest, log)),
			harness.Printed())
	}

	run(rest)

	sources, linked := log.next(t)
	assert.Equal(t, []string{"a.c", "b.c", "c.cpp"}, sources)
	assert.True(t, linked)

	run(rest)

	sources, linked = log.next(t)
	assert.Empty(t, sources, "nothing changed")
	assert.False(t, linked, "nothing changed")

	writeFile(t, harness, "include/common.h", "#define COMMON 3\n")
	run(rest)

	sources, linked = log.next(t)
	assert.Equal(t, []string{"a.c"}, sources, "a header of a.c")
	assert.True(t, linked)

	// The core would build a block that changed afresh; the backend alone
	// compiles the objects whose flags changed.
	rest["cflags"] = []any{"-Wall"}
	run(rest)

	sources, _ = log.next(t)
	assert.Equal(t, []string{"a.c", "b.c"}, sources, "cflags")

	rest["sources"] = []any{"a.c", "c.cpp"}
	run(rest)

	sources, linked = log.next(t)
	assert.Empty(t, sources, "a source dropped")
	assert.True(t, linked, "a source dropped")
	assert.NoFileExists(t, filepath.Join(harness.Context.Dirs.Work, "obj", objectName("b.c")))
	assert.FileExists(t, filepath.Join(harness.Context.Dirs.Work, "obj", objectName("a.c")))
}
