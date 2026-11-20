//go:build integration

package cc

import (
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/sdk/build/v3/buildtest"
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
		Executable: "",
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

	hostBuild(t, harness, C(),
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

// TestCcShowsTheWarnings proves with a real compiler that its warnings are
// shown, and shown again by a build that takes the object from the cache.
func TestCcShowsTheWarnings(t *testing.T) {
	t.Parallel()

	includeDir, _ := requireHostHeaders(t)

	harness := hostHarness(t, includeDir, map[string]string{
		"warn.c": "int warn(void) { int unused; return 0; }\n",
	})
	rest := map[string]any{"module": "warn", "sources": []any{"warn.c"}, "cflags": []any{"-Wall"}}

	hostBuild(t, harness, C(), rest, "warn")

	warnings := harness.Warnings()
	require.Len(t, warnings, 1)
	assert.True(t, strings.HasPrefix(warnings[0], "compiling warn.c:\n"), warnings[0])
	assert.Contains(t, warnings[0], "unused")

	hostBuild(t, harness, C(), rest, "warn")
	assert.Equal(t, []string{warnings[0], warnings[0]}, harness.Warnings())
}

// symbolicHostC is a program that exports shadowed(), as Tarantool exports
// its functions, loads the module of its argument and prints what the
// call_shadowed() of the module returns.
const symbolicHostC = `#include <dlfcn.h>
#include <stdio.h>
int shadowed(void) { return 1; }
int main(int argc, char **argv) {
	void *module = dlopen(argv[1], RTLD_NOW);
	if (module == NULL) { fprintf(stderr, "%s\n", dlerror()); return 1; }
	int (*call)(void) = (int (*)(void))dlsym(module, "call_shadowed");
	printf("%d\n", call());
	return 0;
}
`

// TestCcBindsTheCallsOfTheModuleToItself proves that a module of Linux
// calls its own function of a name the executable exports as well, as a
// module defining curl_easy_init does in Tarantool Enterprise, which
// exports the functions of curl.
func TestCcBindsTheCallsOfTheModuleToItself(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("-Bsymbolic-functions is of the link of Linux")
	}

	includeDir, _ := requireHostHeaders(t)

	harness := hostHarness(t, includeDir, map[string]string{
		"shadow.c": "int shadowed(void) { return 2; }\n" +
			"int call_shadowed(void) { return shadowed(); }\n",
		"host.c": symbolicHostC,
	})

	hostBuild(t, harness, C(), map[string]any{"module": "shadow", "sources": []any{"shadow.c"}},
		"shadow")

	cwd := harness.Context.Dirs.Cwd
	host := filepath.Join(cwd, "host")

	printed, err := exec.CommandContext(t.Context(), "cc", "-rdynamic", "-o", host,
		filepath.Join(cwd, "host.c"), "-ldl").CombinedOutput()
	require.NoError(t, err, string(printed))

	printed, err = exec.CommandContext(t.Context(), host,
		filepath.Join(harness.Context.Dirs.Modules, "shadow.so")).CombinedOutput()
	require.NoError(t, err, string(printed))
	assert.Equal(t, "2", strings.TrimSpace(string(printed)), "the module calls its own")
}

// TestCcWarnsOfTheSymbolsOfTarantool proves that a module exporting a
// function of the name of one the tarantool of the build exports is warned
// of, and one that makes it static is not. The tarantool is a program that
// exports shadowed(), as Tarantool exports its functions.
func TestCcWarnsOfTheSymbolsOfTarantool(t *testing.T) {
	t.Parallel()

	includeDir, _ := requireHostHeaders(t)

	harness := hostHarness(t, includeDir, map[string]string{
		"shadow.c": "int shadowed(void) { return 2; }\nint mine(void) { return shadowed(); }\n",
		"host.c":   symbolicHostC,
	})

	cwd := harness.Context.Dirs.Cwd
	host := filepath.Join(cwd, "host")

	printed, err := exec.CommandContext(t.Context(), "cc", "-rdynamic", "-o", host,
		filepath.Join(cwd, "host.c"), "-ldl").CombinedOutput()
	require.NoError(t, err, string(printed))

	harness.Context.Tarantool.Executable = host

	rest := map[string]any{"module": "shadow", "sources": []any{"shadow.c"}}

	hostBuild(t, harness, C(), rest, "shadow")

	warnings := harness.Warnings()
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "exports 1 symbols that the tarantool "+host+
		" exports too: shadowed;")

	// No warning of a module up to date, which is not linked again.
	hostBuild(t, harness, C(), rest, "shadow")
	assert.Len(t, harness.Warnings(), 1)

	writeFile(t, harness, "shadow.c",
		"static int shadowed(void) { return 2; }\nint mine(void) { return shadowed(); }\n")
	hostBuild(t, harness, C(), rest, "shadow")
	assert.Len(t, harness.Warnings(), 1, "a static function is no clash")
}

// TestCcLoadsTheModuleIntoTarantool proves with the host's tarantool that a
// module just linked is loaded into it: a lua-c module and a c one that
// load build, and a module that needs a function no library of the link
// has fails its build with what the loader says.
func TestCcLoadsTheModuleIntoTarantool(t *testing.T) {
	t.Parallel()

	includeDir, tarantool := requireHostHeaders(t)

	harness := hostHarness(t, includeDir, map[string]string{
		"fast_hash.c": fastHashC,
		"answer.c":    "int answer(void) { return 42; }\n",
		"missing.c": "int no_such_function_of_tt(void);\n" +
			"int missing(void) { return no_such_function_of_tt(); }\n",
	})

	harness.Context.Tarantool.Executable = tarantool

	hostBuild(t, harness, LuaC(),
		map[string]any{"module": "fast_hash", "sources": []any{"fast_hash.c"}}, "fast_hash")
	hostBuild(t, harness, C(),
		map[string]any{"module": "answer", "sources": []any{"answer.c"}}, "answer")

	bld, err := buildtest.Validate(C(), flatComponent(),
		block(map[string]any{"module": "missing", "sources": []any{"missing.c"}}, nil))
	require.NoError(t, err)

	err = harness.Run(t.Context(), bld)
	require.ErrorIs(t, err, errNoLoad)
	assert.Contains(t, err.Error(), "the module does not load into "+tarantool)
	assert.Contains(t, err.Error(), "no_such_function_of_tt")
}

// TestCcKeepsTheProjectOutOfTheModule proves with the compilers of the host
// that a module has debug information, and that it names no path of the
// project: -ffile-prefix-map makes the project ".", whose sources are found
// from a debugger started there.
func TestCcKeepsTheProjectOutOfTheModule(t *testing.T) {
	t.Parallel()

	includeDir, _ := requireHostHeaders(t)
	requireCXX(t)

	harness := hostHarness(t, includeDir, map[string]string{
		"a.c":     "#include \"inc/a.h\"\nint a(void) { return A; }\n",
		"b.cpp":   "extern \"C\" int b() { return 2; }\n",
		"inc/a.h": "#define A 1\n",
	})
	facts := harness.Context

	// The compiles run in the project, which -g records the directory of.
	require.True(t, strings.HasPrefix(facts.Dirs.Cwd, facts.Dirs.Project))

	hostBuild(t, harness, C(), map[string]any{"module": "paths", "sources": []any{"a.c", "b.cpp"}},
		"paths")

	module := filepath.Join(facts.Dirs.Modules, "paths"+facts.Target.ModuleExt())

	data, err := os.ReadFile(module)
	require.NoError(t, err)

	// On macOS the debug information stays in the objects, and the debug
	// map of the module names them, under Work, as dsymutil wants them:
	// -ffile-prefix-map does not reach those paths.
	if runtime.GOOS != "linux" {
		return
	}

	assert.NotContains(t, string(data), harness.Context.Dirs.Project)
	assert.NotContains(t, string(data), harness.Context.Dirs.Work)

	file, err := elf.Open(module)
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	assert.NotNil(t, file.Section(".debug_info"), "-g by default")
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

// TestLuaCMixedModuleOfZig builds the module of mixedSources with zig cc and
// zig c++ for the host, static = true on Linux, and loads it in the host's
// Tarantool: zig c++ links its own libc++ in, so the link gets neither
// -static-libstdc++ nor -Wl,--exclude-libs, which zig refuses, and the module
// needs no C++ library. zig writes the depfile of each compile, so a build
// with nothing changed compiles nothing.
func TestLuaCMixedModuleOfZig(t *testing.T) {
	t.Parallel()

	includeDir, tarantool := requireHostHeaders(t)

	zig, err := exec.LookPath("zig")
	if err != nil {
		t.Skipf("zig not found in PATH: %v", err)
	}

	harness := hostHarness(t, includeDir, mixedSources)

	harness.Context.Static = runtime.GOOS == "linux"

	log := &wrapLog{path: filepath.Join(t.TempDir(), "wrap.log"), read: 0}
	compilers := map[string]string{"CC": zig + " cc", "CXX": zig + " c++"}
	run := func() {
		t.Helper()

		bld := wrappedBuildOf(t, harness, LuaC(), mixedBlock(), log, compilers)

		require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())
	}

	run()

	sources, linked := log.next(t)
	assert.Equal(t, []string{"cpp/twice.cc", "cpp/vec.cpp", "mixed.c"}, sources)
	assert.True(t, linked)

	for _, object := range []string{"mixed.c", "cpp/vec.cpp", "cpp/twice.cc"} {
		assert.FileExists(t, filepath.Join(harness.Context.Dirs.Work, "obj",
			objectName(object)+".json"), "zig wrote the depfile of %s", object)
	}

	printed, err := exec.CommandContext(t.Context(), tarantool,
		filepath.Join(harness.Context.Dirs.Cwd, "check.lua"),
		harness.Context.Dirs.Modules).CombinedOutput()
	require.NoError(t, err, string(printed))
	assert.Equal(t, "55\t-1\t42", strings.TrimSpace(string(printed)))

	if runtime.GOOS == "linux" {
		module, err := elf.Open(filepath.Join(harness.Context.Dirs.Modules, "mixed.so"))
		require.NoError(t, err)

		needed, err := module.ImportedLibraries()
		require.NoError(t, module.Close())
		require.NoError(t, err)

		for _, library := range needed {
			assert.NotContains(t, library, "libstdc++")
			assert.NotContains(t, library, "libc++")
		}
	}

	run()

	sources, linked = log.next(t)
	assert.Empty(t, sources, "nothing changed")
	assert.False(t, linked, "nothing changed")
}

// TestLuaCModuleOfZigForMacOS builds a lua-c module for macOS with zig cc on a
// host of any OS, as zig links for another: its linker, which crashes on
// -bundle, links a dylib, and names it @rpath/m.dylib rather than by the
// path of its -o, one of the machine it is linked on.
func TestLuaCModuleOfZigForMacOS(t *testing.T) {
	t.Parallel()

	zig, err := exec.LookPath("zig")
	if err != nil {
		t.Skipf("zig not found in PATH: %v", err)
	}

	// The module declares what it calls of Lua: no header of Tarantool is
	// read.
	includeDir := filepath.Join(t.TempDir(), "include", "tarantool")
	require.NoError(t, os.MkdirAll(includeDir, 0o750))

	harness := hostHarness(t, includeDir, map[string]string{
		"m.c": "int lua_settop(void *, int);\n" +
			"int luaopen_m(void *L) { lua_settop(L, 0); return 0; }\n",
	})

	harness.Context.Target = build.Target{OS: "darwin", Arch: "arm64"}

	log := &wrapLog{path: filepath.Join(t.TempDir(), "wrap.log"), read: 0}
	bld := wrappedBuildOf(t, harness, LuaC(),
		map[string]any{"module": "m", "sources": []any{"m.c"}}, log,
		map[string]string{"CC": zig + " cc -target aarch64-macos"})

	// zig keeps what it builds for a target in its global cache, so the
	// test takes long once.
	require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())

	module := filepath.Join(harness.Context.Dirs.Modules, "m.dylib")

	file, err := macho.Open(module)
	require.NoError(t, err)

	kind, cpu := file.Type, file.Cpu

	require.NoError(t, file.Close())
	assert.Equal(t, macho.TypeDylib, kind)
	assert.Equal(t, macho.CpuArm64, cpu)

	id, err := machoDylibID(module)
	require.NoError(t, err)
	assert.Equal(t, "@rpath/m.dylib", id)
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

	compilers := map[string]string{"CC": "cc", "CXX": "c++"}

	for _, variable := range []string{"CC", "CXX"} {
		if value := os.Getenv(variable); value != "" {
			compilers[variable] = value
		}
	}

	return wrappedBuildOf(t, harness, C(), rest, log, compilers)
}

// wrappedBuildOf is the build of rest of backend in harness with each
// compiler of compilers, CC and CXX, behind wrapper, which logs into
// log.path.
func wrappedBuildOf(t *testing.T, harness *buildtest.Harness, backend build.Backend,
	rest map[string]any, log *wrapLog, compilers map[string]string,
) ccBuild {
	t.Helper()

	validated, err := buildtest.Validate(backend, flatComponent(), block(rest, nil))
	require.NoError(t, err)

	bld, ok := validated.(ccBuild)
	require.True(t, ok)

	// Written once: the wrapper written again is another compiler, its
	// time of change another.
	if _, set := harness.Context.Env["WRAP_LOG"]; !set {
		writeTool(t, harness, "wrap", wrapper)
	}

	harness.Context.Env["WRAP_LOG"] = log.path

	environ := map[string]string{}

	for variable, value := range compilers {
		environ[variable] = "wrap " + value
	}

	bld.lookupEnv = func(key string) (string, bool) {
		value, set := environ[key]

		return value, set
	}

	return bld
}

// next returns the sources the compiles logged since the last call compiled,
// sorted, and whether a link was logged; the probes of the compilers and
// of their flags are left out.
func (l *wrapLog) next(t *testing.T) ([]string, bool) {
	t.Helper()

	data, err := os.ReadFile(l.path)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")

	var ran []string

	for _, line := range lines[l.read:] {
		if !strings.HasSuffix(line, " -dumpmachine") && !strings.HasSuffix(line, " --version") &&
			!isFlagProbe(line) {
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

// TestLuaCChecksTheModule checks the check of a lua-c module with the
// compilers of the host: a C++ source that includes lua.h fails the build
// with the hint of lua.hpp, a module of another name than its entry point
// fails with the name it wants, and one of a namespace wants the entry
// point of <namespace>.<module>.
func TestLuaCChecksTheModule(t *testing.T) {
	t.Parallel()

	includeDir, _ := requireHostHeaders(t)
	requireCXX(t)

	cxx := hostHarness(t, includeDir, map[string]string{
		"plain.cpp": "#include <lua.h>\n#include <lauxlib.h>\n\n" +
			"extern \"C\" int luaopen_plain(lua_State *L) {\n" +
			"\tluaL_checkstack(L, 1, \"no room\");\n\tlua_pushinteger(L, 1);\n\treturn 1;\n}\n",
	})

	bld, err := buildtest.Validate(LuaC(), flatComponent(),
		block(map[string]any{"module": "plain", "sources": []any{"plain.cpp"}}, nil))
	require.NoError(t, err)

	err = cxx.Run(t.Context(), bld)
	require.ErrorIs(t, err, errCxxLuaAPI)
	assert.Contains(t, err.Error(), "_Z15luaL_checkstackP9lua_StateiPKc")
	assert.Contains(t, err.Error(), "<lua.hpp>")

	// The record of the link is written after the check: the build fails
	// again rather than taking the module for up to date.
	require.ErrorIs(t, cxx.Run(t.Context(), bld), errCxxLuaAPI)

	wrong := hostHarness(t, includeDir, map[string]string{"fast_hash.c": fastHashC})

	bld, err = buildtest.Validate(LuaC(), flatComponent(),
		block(map[string]any{"module": "fast", "sources": []any{"fast_hash.c"}}, nil))
	require.NoError(t, err)

	err = wrong.Run(t.Context(), bld)
	require.ErrorIs(t, err, errNoEntry)
	assert.Contains(t, err.Error(), `exports no luaopen_fast, the function require("fast")`)

	// The same block is a c build, which Tarantool loads by its functions.
	bld, err = buildtest.Validate(C(), flatComponent(),
		block(map[string]any{"module": "fast", "sources": []any{"fast_hash.c"}}, nil))
	require.NoError(t, err)
	require.NoError(t, wrong.Run(t.Context(), bld))

	namespaced := buildtest.New(t, LuaC(), component())

	namespaced.SetTarantool(build.Tarantool{
		Prefix: filepath.Dir(filepath.Dir(includeDir)), IncludeDir: includeDir, Version: "",
		Executable: "",
	})
	writeFile(t, namespaced, "hash.c", strings.ReplaceAll(fastHashC, "luaopen_fast_hash",
		"luaopen_demo_hash"))

	bld, err = buildtest.Validate(LuaC(), component(),
		block(map[string]any{"module": "hash", "sources": []any{"hash.c"}}, nil))
	require.NoError(t, err)
	require.NoError(t, namespaced.Run(t.Context(), bld), "require('demo.hash')")
}

// TestReadSymbolsOfMachO reads the symbols of Mach-O modules zig links for
// macOS, thin and universal, and checks them as a lua-c module is checked:
// the Mach-O half of the check, on a host of another OS.
func TestReadSymbolsOfMachO(t *testing.T) {
	t.Parallel()

	_, err := exec.LookPath("zig")
	if err != nil {
		t.Skipf("zig not found in PATH: %v", err)
	}

	dir := t.TempDir()
	source := filepath.Join(dir, "m.c")

	// cxx_api is the C++ name of luaL_checkstack, which a C++ source that
	// includes lua.h needs; Mach-O names a symbol of C with a leading _.
	require.NoError(t, os.WriteFile(source, []byte(
		"extern int cxx_api(void) __asm__(\"__Z15luaL_checkstackP9lua_StateiPKc\");\n"+
			"int lua_settop(void *, int);\n"+
			"int luaopen_m(void *L) { return lua_settop(L, 0) + cxx_api(); }\n"), 0o600))

	thin := map[string]string{}

	for _, arch := range []string{"x86_64", "aarch64"} {
		out := filepath.Join(dir, arch+".dylib")

		// zig keeps what it builds for a target in its global cache, so
		// the test takes long once.
		printed, err := exec.CommandContext(t.Context(), "zig", "cc", "-target", arch+"-macos",
			"-shared", "-Wl,-undefined,dynamic_lookup", "-o", out, source).CombinedOutput()
		require.NoError(t, err, string(printed))

		thin[arch] = out
	}

	symbols, err := readSymbols(thin["x86_64"])
	require.NoError(t, err)
	require.Len(t, symbols, 1)
	assert.Contains(t, symbols[0].defined, "luaopen_m")
	assert.Contains(t, symbols[0].undefined, "lua_settop")
	assert.Contains(t, symbols[0].undefined, "_Z15luaL_checkstackP9lua_StateiPKc")

	err = checkLuaModule(thin["x86_64"], "m", symbols)
	require.ErrorIs(t, err, errCxxLuaAPI)
	require.NotErrorIs(t, err, errNoEntry)

	fat := filepath.Join(dir, "fat.dylib")
	writeFat(t, fat, thin["x86_64"], thin["aarch64"])

	symbols, err = readSymbols(fat)
	require.NoError(t, err)
	require.Len(t, symbols, 2)
	assert.NotEmpty(t, symbols[0].arch)
	assert.NotEqual(t, symbols[0].arch, symbols[1].arch)

	err = checkLuaModule(fat, "other", symbols)
	require.ErrorIs(t, err, errNoEntry)
	assert.Contains(t, err.Error(), fat+" ("+symbols[0].arch+")")
}

// writeFat writes into path the universal Mach-O file of the thin files of
// slices, as lipo -create does: the fat header and its table of the slices,
// big-endian, then each slice at an offset aligned to a page.
func writeFat(t *testing.T, path string, slices ...string) {
	t.Helper()

	const (
		fatMagic  = 0xcafebabe
		pageAlign = 14 // 2^14, the page of arm64.
	)

	var header []byte

	bodies := make([][]byte, 0, len(slices))
	be32 := func(value uint32) {
		header = binary.BigEndian.AppendUint32(header, value)
	}

	be32(fatMagic)
	be32(uint32(len(slices))) //nolint:gosec // A few slices.

	offset := uint32(1 << pageAlign)

	for _, slice := range slices {
		file, err := macho.Open(slice)
		require.NoError(t, err)

		cpu, sub := file.Cpu, file.SubCpu

		require.NoError(t, file.Close())

		body, err := os.ReadFile(slice)
		require.NoError(t, err)

		be32(uint32(cpu))
		be32(sub)
		be32(offset)
		be32(uint32(len(body))) //nolint:gosec // A test module is small.
		be32(pageAlign)

		bodies = append(bodies, body)

		offset += (uint32(len(body)) + 1<<pageAlign - 1) &^ (1<<pageAlign - 1) //nolint:gosec
	}

	data := header

	for _, body := range bodies {
		data = append(data, make([]byte, (1<<pageAlign-len(data)%(1<<pageAlign))%(1<<pageAlign))...)
		data = append(data, body...)
	}

	require.NoError(t, os.WriteFile(path, data, 0o600))
}
