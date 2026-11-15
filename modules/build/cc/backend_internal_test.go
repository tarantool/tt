package cc

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// component is a component the tests build, which wants its build to make
// results of the types wants (build.Component.Wants).
func component(wants ...build.Result) build.Component {
	return build.Component{
		Name: "native", Path: "native", Namespace: "demo", Package: "demo", Wants: wants,
	}
}

// block is a build block with the fields rest of the backend and env.
func block(rest map[string]any, env map[string]string) buildtest.Block {
	return buildtest.Block{
		Common:      build.Common{Cwd: "", Env: env, Output: nil, Static: false},
		Incremental: false,
		Fields:      rest,
	}
}

// module is the least block of a module m: its module and its sources, with
// the fields of extra.
func module(extra map[string]any) map[string]any {
	rest := map[string]any{"module": "m", "sources": []any{"m.c"}}

	maps.Copy(rest, extra)

	return rest
}

func TestBackends(t *testing.T) {
	t.Parallel()

	backends := Backends()
	require.Len(t, backends, 2)

	for i, name := range []string{CName, LuaCName} {
		backend := backends[i]

		assert.Equal(t, name, backend.Name)
		assert.Equal(t, []build.Result{build.Module}, backend.Produces)
		assert.False(t, backend.NoSourceLayout, name)
		assert.False(t, backend.Incremental, name)
		assert.False(t, backend.Hook, name)
		assert.Equal(t, build.OutputNone, backend.OutputFrom, name)
		assert.Empty(t, backend.Exclude, name)
	}

	fields, err := C().Fields()
	require.NoError(t, err)
	assert.Equal(t, []string{
		"module", "sources", "include_dirs", "libraries", "library_dirs", "rpath", "pkg_config",
		"link", "defines", "cflags", "cxxflags", "ldflags", "platforms",
	}, fields)
}

func TestConfig(t *testing.T) {
	t.Parallel()

	cfg, unknown, err := LuaC().Decode(map[string]any{
		"module":       "fast",
		"sources":      []any{"a.c", "b.cpp"},
		"include_dirs": []any{"include"},
		"libraries":    []any{"m"},
		"library_dirs": []any{"/opt/lib"},
		"rpath":        []any{"$ORIGIN"},
		"defines":      []any{"NDEBUG"},
		"cflags":       []any{"-Wall"},
		"cxxflags":     []any{"-std=c++17"},
		"ldflags":      []any{"-Wl,--as-needed"},
		"platforms": map[string]any{
			"linux": map[string]any{
				"libraries": []any{"rt"}, "cflags": []any{"-pthread"},
				"cxxflags": []any{"-pthread"}, "rpath": []any{"$ORIGIN/lib"},
			},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, unknown)
	assert.Equal(t, &Config{
		Module:      "fast",
		Sources:     []string{"a.c", "b.cpp"},
		IncludeDirs: []string{"include"},
		Libraries:   []string{"m"},
		LibraryDirs: []string{"/opt/lib"},
		Rpath:       []string{"$ORIGIN"},
		PkgConfig:   nil,
		Link:        nil,
		Defines:     []string{"NDEBUG"},
		Cflags:      []string{"-Wall"},
		Cxxflags:    []string{"-std=c++17"},
		Ldflags:     []string{"-Wl,--as-needed"},
		Platforms: map[string]Overlay{
			"linux": {
				IncludeDirs: nil, Libraries: []string{"rt"}, LibraryDirs: nil,
				Rpath: []string{"$ORIGIN/lib"}, PkgConfig: nil, Defines: nil,
				Cflags: []string{"-pthread"}, Cxxflags: []string{"-pthread"}, Ldflags: nil,
			},
		},
	}, cfg)

	for _, backend := range Backends() {
		_, err := buildtest.Validate(backend, component(), block(module(nil), nil))
		require.NoError(t, err, backend.Name)
	}
}

func TestValidateErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		rest  map[string]any
		env   map[string]string
		field string
		msg   string
	}{
		{"no module", map[string]any{"sources": []any{"m.c"}}, nil,
			"", `module is required for backend "%s"`},
		{"no sources", map[string]any{"module": "m"}, nil,
			"", `sources is required for backend "%s"`},
		{"empty sources", map[string]any{"module": "m", "sources": []any{}}, nil,
			"", `sources is required for backend "%s"`},
		{"CC in env", module(nil), map[string]string{"CC": "clang"},
			"env.CC", `does nothing on backend "%s": the compiler is CC of the environment ` +
				"tt runs in, cc by default"},
		{"CFLAGS in env", module(nil), map[string]string{"CFLAGS": "-O0"},
			"env.CFLAGS", `does nothing on backend "%s": put the flags of the compiler into ` +
				"cflags; CFLAGS of the environment tt runs in comes after them"},
		{"CXX in env", module(nil), map[string]string{"CXX": "clang++"},
			"env.CXX", `does nothing on backend "%s": the C++ compiler is CXX of the ` +
				"environment tt runs in, c++ by default"},
		{"CXXFLAGS in env", module(nil), map[string]string{"CXXFLAGS": "-O0"},
			"env.CXXFLAGS", `does nothing on backend "%s": put the flags of the C++ compiler ` +
				"into cxxflags; CXXFLAGS of the environment tt runs in comes after them"},
		{"CPPFLAGS in env", module(nil), map[string]string{"CPPFLAGS": "-DX"},
			"env.CPPFLAGS", `does nothing on backend "%s": put the macros into defines, the ` +
				"directories of headers into include_dirs and other flags of the preprocessor " +
				"into cflags and cxxflags"},
		{"a source of no language", map[string]any{
			"module": "m", "sources": []any{"m.c", "m.h"},
		}, nil, "sources[1]", `"m.h" is not a source tt compiles: want a name ending in ` +
			".c, .cc, .cpp, .cxx, .c++, .S or .s; a prebuilt library goes into libraries, " +
			"by name or as a file"},
		{"an archive in sources", map[string]any{
			"module": "m", "sources": []any{"vendor/libz.a"},
		}, nil, "sources[0]", `"vendor/libz.a" is not a source tt compiles: want a name ` +
			"ending in .c, .cc, .cpp, .cxx, .c++, .S or .s; a prebuilt library goes into " +
			"libraries, by name or as a file"},
		{"an empty library", module(map[string]any{"libraries": []any{"m", ""}}), nil,
			"libraries", "must not contain an empty entry"},
		{"a source listed twice", map[string]any{
			"module": "m", "sources": []any{"a.c", "b.cc", "./a.c"},
		}, nil, "sources[2]", `"./a.c" is sources[0] again`},
		{"an empty cxxflag", module(map[string]any{"cxxflags": []any{""}}), nil,
			"cxxflags", "must not contain an empty entry"},
		{"an empty cxxflag of a platform", module(map[string]any{
			"platforms": map[string]any{"linux": map[string]any{"cxxflags": []any{""}}},
		}), nil, "platforms.linux.cxxflags", "must not contain an empty entry"},
		{"LDFLAGS in env", module(nil), map[string]string{"LDFLAGS": "-s"},
			"env.LDFLAGS", `does nothing on backend "%s": put the flags of the link into ` +
				"ldflags; LDFLAGS of the environment tt runs in comes after them"},
		{"LIBS in env", module(nil), map[string]string{"LIBS": "-lm"},
			"env.LIBS", `does nothing on backend "%s": put the libraries into libraries and ` +
				"their directories into library_dirs"},
		{"LDLIBS in env", module(nil), map[string]string{"LDLIBS": "-lm"},
			"env.LDLIBS", `does nothing on backend "%s": put the libraries into libraries and ` +
				"their directories into library_dirs"},
		{"PKG_CONFIG in env", module(nil), map[string]string{"PKG_CONFIG": "pkgconf"},
			"env.PKG_CONFIG", `does nothing on backend "%s": pkg-config is PKG_CONFIG of the ` +
				"environment tt runs in, pkg-config by default; PKG_CONFIG_PATH in env reaches it"},
		{"an empty pkg_config", module(map[string]any{"pkg_config": []any{"zlib", ""}}), nil,
			"pkg_config", "must not contain an empty entry"},
		{"an empty pkg_config of a platform", module(map[string]any{
			"platforms": map[string]any{"darwin": map[string]any{"pkg_config": []any{""}}},
		}), nil, "platforms.darwin.pkg_config", "must not contain an empty entry"},
		{"a link of no name", module(map[string]any{"link": []any{"Foo"}}), nil,
			"link[0]", `"Foo" is not the name of a component`},
		{"a link of itself", module(map[string]any{"link": []any{"foo", "native"}}), nil,
			"link[1]", `"native" is the component itself`},
		{"a link twice", module(map[string]any{"link": []any{"foo", "foo"}}), nil,
			"link[1]", `"foo" is listed twice`},
		{"a platform of no OS", module(map[string]any{
			"platforms": map[string]any{"macos": map[string]any{}},
		}), nil, "platforms.macos", `"macos" is not an OS tt builds for (want linux or darwin)`},
		{"an empty cflag", module(map[string]any{"cflags": []any{"-Wall", ""}}), nil,
			"cflags", "must not contain an empty entry"},
		{"an empty ldflag", module(map[string]any{"ldflags": []any{""}}), nil,
			"ldflags", "must not contain an empty entry"},
		{"an empty cflag of a platform", module(map[string]any{
			"platforms": map[string]any{"linux": map[string]any{"cflags": []any{""}}},
		}), nil, "platforms.linux.cflags", "must not contain an empty entry"},
		{"an empty ldflag of a platform", module(map[string]any{
			"platforms": map[string]any{"darwin": map[string]any{"ldflags": []any{""}}},
		}), nil, "platforms.darwin.ldflags", "must not contain an empty entry"},
		{"an empty rpath", module(map[string]any{"rpath": []any{"$ORIGIN", ""}}), nil,
			"rpath", "must not contain an empty entry"},
		{"a relative rpath", module(map[string]any{"rpath": []any{"lib"}}), nil,
			"rpath[0]", `"lib" is relative, which the loader resolves against the working ` +
				"directory of the process: write $ORIGIN/lib for a directory beside the " +
				"module, or an absolute one"},
		{"an rpath under $ORIGINAL", module(map[string]any{"rpath": []any{"$ORIGINAL"}}), nil,
			"rpath[0]", `"$ORIGINAL" is relative, which the loader resolves against the ` +
				"working directory of the process: write $ORIGIN/$ORIGINAL for a directory " +
				"beside the module, or an absolute one"},
		{"an rpath with a comma", module(map[string]any{
			"platforms": map[string]any{"linux": map[string]any{"rpath": []any{"/a,b"}}},
		}), nil, "platforms.linux.rpath[0]", `"/a,b" has a comma, which -Wl,-rpath,<dir> ` +
			"would split"},
		{"cflags of a wrong type", module(map[string]any{"cflags": "-Wall"}), nil,
			"cflags", "must be an array, not a string"},
		{"a source of a wrong type", map[string]any{"module": "m", "sources": []any{int64(1)}},
			nil, "sources[0]", "must be a string, not an integer"},
		{"a platform of a wrong type", module(map[string]any{
			"platforms": map[string]any{"linux": []any{}},
		}), nil, "platforms.linux", "must be a table, not an array"},
		{"an unknown field", module(map[string]any{"make_target": "all"}), nil,
			"make_target", "unknown field"},
		{"an unknown field of a platform", module(map[string]any{
			"platforms": map[string]any{"linux": map[string]any{"sources": []any{"x.c"}}},
		}), nil, "platforms.linux.sources", "unknown field"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			for _, backend := range Backends() {
				_, err := buildtest.Validate(backend, component(),
					block(test.rest, test.env))

				var fieldErr *build.FieldError

				require.ErrorAs(t, err, &fieldErr, backend.Name)
				assert.Equal(t, test.field, fieldErr.Field, backend.Name)

				// %s in a message is the name of the backend.
				msg := strings.ReplaceAll(test.msg, "%s", backend.Name)
				assert.Equal(t, msg, fieldErr.Msg, backend.Name)
			}
		})
	}
}

// TestWants checks that neither backend builds a component that wants
// programs only nor runs a hook.
func TestWants(t *testing.T) {
	t.Parallel()

	for _, backend := range Backends() {
		_, err := buildtest.Validate(backend, component(build.Program),
			block(module(nil), nil))

		var fieldErr *build.FieldError

		require.ErrorAs(t, err, &fieldErr)
		assert.Equal(t, `backend "`+backend.Name+`" makes nothing the component wants`,
			fieldErr.Msg)

		_, err = buildtest.ValidateHook(backend, block(module(nil), nil))
		require.ErrorAs(t, err, &fieldErr)
		assert.Equal(t, `backend "`+backend.Name+`" runs no hook`, fieldErr.Msg)
	}
}

// TestMissingHeaders checks that a build without the headers of Tarantool
// fails its preflight, and its run before the compiler is invoked: CC is a
// program that would fail loudly.
func TestMissingHeaders(t *testing.T) {
	t.Parallel()

	validated, err := buildtest.Validate(C(), component(), block(module(nil), nil))
	require.NoError(t, err)

	bld, ok := validated.(ccBuild)
	require.True(t, ok)

	bld.lookupEnv = func(key string) (string, bool) { return "/nonexistent/cc", key == "CC" }

	// The harness knows no Tarantool, and so no prefix of its headers.
	harness := buildtest.New(t, C(), component())

	preflighter := build.Preflighter(bld)

	err = preflighter.Preflight(t.Context(), harness.Context)
	require.ErrorIs(t, err, ErrMissingHeaders)
	assert.Contains(t, err.Error(), "tarantool development headers not found")
	assert.Contains(t, err.Error(), "tt binary install tarantool installs one")

	err = bld.Run(t.Context(), harness.Context)
	require.ErrorIs(t, err, ErrMissingHeaders)
	assert.Empty(t, harness.Printed())

	// A Tarantool without headers: the core says where it looked for them.
	harness.SetTarantool(build.Tarantool{
		Prefix: "/tt", IncludeDir: "", Version: "", Executable: "",
	})

	err = preflighter.Preflight(t.Context(), harness.Context)
	require.ErrorIs(t, err, ErrMissingHeaders)
	require.ErrorIs(t, err, build.ErrNoHeaders)
	assert.NotContains(t, err.Error(), "tt binary install tarantool installs one")

	harness.SetTarantool(build.Tarantool{
		Prefix: "/tt", IncludeDir: "/tt/include/tarantool", Version: "", Executable: "",
	})
	require.NoError(t, preflighter.Preflight(t.Context(), harness.Context))
}

// fakeCC is a compiler that records each run as a line of
// $TT_BUILD_DIR/calls - its name and its arguments - writes the file of its
// -o, a copy of the source of its -c and its arguments, and writes the file
// of its -MF as a list of the source and of what the file <source>.deps
// lists, the headers it would have read. It prints the file <source>.warn,
// its warnings, to stderr.
const fakeCC = `#!/bin/sh
args="$*"
# One write a run: the compiles run at once.
printf '%s\n' "${0##*/} $args" >> "$TT_BUILD_DIR/calls"
while [ $# -gt 0 ]; do
	case "$1" in
	-o) out=$2; shift ;;
	-MF) dep=$2; shift ;;
	-c) src=$2; shift ;;
	esac
	shift
done
if [ -n "$out" ]; then
	{ if [ -n "$src" ]; then cat "$src"; fi; printf '%s\n' "$args"; } > "$out"
fi
if [ -n "$dep" ]; then
	{
		printf 'x.o: %s' "$src"
		if [ -f "$src.deps" ]; then printf ' %s' "$(cat "$src.deps")"; fi
		printf '\n'
	} > "$dep"
fi
if [ -n "$src" ] && [ -f "$src.warn" ]; then cat "$src.warn" >&2; fi
`

// fakeHarness is a harness of backend that knows the Tarantool of the
// prefix /tt, with fakeCC installed as fakecc and fakec++ in the bin
// directory of its project, and the build of the block rest of backend in
// it, which reads environ as the environment tt runs in.
func fakeHarness(t *testing.T, backend build.Backend, rest map[string]any,
	environ map[string]string,
) (*buildtest.Harness, ccBuild) {
	t.Helper()

	validated, err := buildtest.Validate(backend, component(), block(rest, nil))
	require.NoError(t, err)

	bld, ok := validated.(ccBuild)
	require.True(t, ok)

	bld.lookupEnv = func(key string) (string, bool) {
		value, set := environ[key]

		return value, set
	}

	harness := buildtest.New(t, backend, component())

	for _, name := range []string{"fakecc", "fakec++"} {
		writeTool(t, harness, name, fakeCC)
	}

	harness.SetTarantool(build.Tarantool{
		Prefix: "/tt", IncludeDir: "/tt/include/tarantool", Version: "", Executable: "",
	})

	return harness, bld
}

// calls are the runs of the fake compilers of harness so far, one a line,
// but the probes of their identity (compilerIdentity) and of their flags
// (prober.probe).
func calls(t *testing.T, harness *buildtest.Harness) []string {
	t.Helper()

	var ran []string

	for _, call := range allCalls(t, harness) {
		if !strings.HasSuffix(call, " -dumpmachine") && !strings.HasSuffix(call, " --version") &&
			!isFlagProbe(call) {
			ran = append(ran, call)
		}
	}

	return ran
}

// isFlagProbe reports whether call is the compile of a probe of a flag.
func isFlagProbe(call string) bool {
	return strings.Contains(call, "/flag_check.c ") || strings.Contains(call, "/flag_check.cpp ")
}

// reproducible are the flags of the compiles of a harness the fake
// compilers take all (reproducibleFlags).
func reproducible(harness *buildtest.Harness) string {
	return strings.Join(reproducibleFlags(harness.Context.Dirs.Project), " ")
}

// allCalls are the runs of the fake compilers of harness so far, one a line.
func allCalls(t *testing.T, harness *buildtest.Harness) []string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(harness.Context.Dirs.Work, "calls"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	require.NoError(t, err)

	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// TestRun checks that a build compiles each source with the compiler of the
// toolchain in the working directory into its object of the work directory,
// and links the module of the extension of the target in the output
// directory: .so, or .dylib on darwin, a dotted module a flat file.
func TestRun(t *testing.T) {
	t.Parallel()

	link := map[string][]string{
		"linux":  {"-shared", "-Wl,-Bsymbolic-functions"},
		"darwin": {"-bundle", "-undefined", "dynamic_lookup", "-all_load"},
	}

	for _, goos := range []string{"linux", "darwin"} {
		harness, bld := fakeHarness(t, LuaC(),
			map[string]any{"module": "a.b", "sources": []any{"a.c"}},
			map[string]string{"CC": "fakecc --wrapped", "CFLAGS": "", "LDFLAGS": ""})
		facts := harness.Context

		facts.Target.OS = goos

		require.NoError(t, harness.Run(t.Context(), bld))

		out := filepath.Join(facts.Dirs.Modules, "a.b"+facts.Target.ModuleExt())
		assert.FileExists(t, out)

		object := filepath.Join(facts.Dirs.Work, "obj", objectName("a.c"))
		assert.FileExists(t, object)
		assert.NoFileExists(t, object+".tmp")

		assert.Equal(t, []string{
			"fakecc --wrapped -fPIC " + reproducible(harness) + " -I/tt/include/tarantool " +
				"-MD -MF " + object + ".d -MQ " + object + " -c a.c -o " + object + ".tmp",
			"fakecc --wrapped " + strings.Join(link[goos], " ") + " -o " + out + " " + object,
		}, calls(t, harness), goos)
	}
}

// TestRunMixed checks that a build of C and C++ compiles each source with
// the compiler and the flags of its language and links with the C++
// compiler, the static C++ library on Linux with static = true.
func TestRunMixed(t *testing.T) {
	t.Parallel()

	harness, bld := fakeHarness(t, C(), map[string]any{
		"module": "m", "sources": []any{"a.c", "sub/b.cpp"},
		"cflags": []any{"-std=c11"}, "cxxflags": []any{"-std=c++17"},
	}, map[string]string{"CC": "fakecc", "CXX": "fakec++", "CXXFLAGS": "-g"})
	facts := harness.Context

	facts.Target.OS = "linux"
	facts.Static = true

	require.NoError(t, harness.Run(t.Context(), bld))

	objA := filepath.Join(facts.Dirs.Work, "obj", objectName("a.c"))
	objB := filepath.Join(facts.Dirs.Work, "obj", objectName("sub/b.cpp"))
	out := filepath.Join(facts.Dirs.Modules, "m.so")

	ran := calls(t, harness)
	require.Len(t, ran, 3)

	// The compiles run at once, in any order.
	slices.Sort(ran[:2])
	assert.Equal(t, []string{
		"fakec++ -fPIC " + reproducible(harness) + " -std=c++17 -g -I/tt/include/tarantool " +
			"-MD -MF " + objB + ".d -MQ " + objB + " -c sub/b.cpp -o " + objB + ".tmp",
		"fakecc -O2 -g -fPIC " + reproducible(harness) + " -std=c11 -I/tt/include/tarantool " +
			"-MD -MF " + objA + ".d -MQ " + objA + " -c a.c -o " + objA + ".tmp",
		"fakec++ -shared -Wl,-Bsymbolic-functions -static-libstdc++ -Wl,--exclude-libs,ALL " +
			"-o " + out + " " + objA + " " + objB,
	}, ran)

	data, err := os.ReadFile(filepath.Join(facts.Dirs.Work, "compile_commands.json"))
	require.NoError(t, err)

	var commands []compileCommand

	require.NoError(t, json.Unmarshal(data, &commands))
	require.Len(t, commands, 2)
	assert.Equal(t, compileCommand{
		Directory: facts.Dirs.Cwd,
		Arguments: slices.Concat([]string{"fakecc", "-O2", "-g", "-fPIC"},
			reproducibleFlags(facts.Dirs.Project), []string{
				"-std=c11", "-I/tt/include/tarantool",
				"-MD", "-MF", objA + ".d", "-MQ", objA, "-c", "a.c", "-o", objA,
			}),
		File:   "a.c",
		Output: objA,
	}, commands[0])
	assert.Equal(t, "sub/b.cpp", commands[1].File)
	assert.Equal(t, "fakec++", commands[1].Arguments[0])
}

// TestRunDropsStaleObjects checks that a build removes the objects of the
// sources it no longer lists, and what a stopped compile left.
func TestRunDropsStaleObjects(t *testing.T) {
	t.Parallel()

	harness, bld := fakeHarness(t, C(), map[string]any{"module": "m", "sources": []any{"a.c"}},
		map[string]string{"CC": "fakecc"})
	objects := filepath.Join(harness.Context.Dirs.Work, "obj")

	require.NoError(t, os.MkdirAll(objects, 0o700))

	for _, name := range []string{"gone.c.12345678.o", "gone.c.12345678.o.d",
		objectName("a.c") + ".tmp"} {
		require.NoError(t, os.WriteFile(filepath.Join(objects, name), nil, 0o600))
	}

	require.NoError(t, harness.Run(t.Context(), bld))

	entries, err := os.ReadDir(objects)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	assert.ElementsMatch(t, []string{objectName("a.c"), objectName("a.c") + ".d"}, names)
}

// failingCC is a compiler that fails with a message of the source of its -c.
const failingCC = `#!/bin/sh
while [ $# -gt 0 ]; do
	if [ "$1" = -c ]; then echo "$2: error: broken" >&2; exit 1; fi
	shift
done
`

// TestRunFailedCompile checks that a failed compile fails the build with
// what the compiler printed, the command and the source in its error, and
// leaves no object and no module.
func TestRunFailedCompile(t *testing.T) {
	t.Parallel()

	harness, bld := fakeHarness(t, C(), map[string]any{"module": "m", "sources": []any{"a.c"}},
		map[string]string{"CC": "failingcc"})

	writeTool(t, harness, "failingcc", failingCC)

	err := harness.Run(t.Context(), bld)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compiling a.c: exit status 1")
	assert.Contains(t, err.Error(), "failingcc -O2 -g -fPIC")
	assert.Contains(t, err.Error(), "a.c: error: broken")

	object := filepath.Join(harness.Context.Dirs.Work, "obj", objectName("a.c"))
	assert.NoFileExists(t, object)
	assert.NoFileExists(t, object+".tmp")
	assert.NoFileExists(t, filepath.Join(harness.Context.Dirs.Modules, "m.so"))
}

// fakePkgConfig is a pkg-config that records each run as a line of
// $TT_BUILD_DIR/calls, knows no package whose name starts with "nosuch",
// and gives flags of its own for the others, those of --cflags with a quoted
// blank, as pkg-config escapes one, and --libs its own with --static.
const fakePkgConfig = `#!/bin/sh
printf '%s\n' "${0##*/} $*" >> "$TT_BUILD_DIR/calls"
static= query=
for arg; do
	case "$arg" in
	nosuch*) echo "Package '$arg' was not found" >&2; exit 1 ;;
	--static) static=1 ;;
	--cflags|--libs) query=$arg ;;
	esac
done
case "$query" in
--cflags) echo '-I/opt/ssl/include -DNAME=a\ b' ;;
--libs) if [ -n "$static" ]; then echo '-lssl -lcrypto -ldl'; else echo '-lssl'; fi ;;
esac
`

// TestRunPkgConfig checks that a build asks pkg-config for the packages of
// pkg_config, an entry an argument of its own, the overlay of the target
// after the build's, and takes its --cflags into every compile and its
// --libs into the link, both with --static for a static build; that a
// package pkg-config does not know fails the preflight naming the entry;
// and that pkg-config not being there is build.ErrNoTool.
func TestRunPkgConfig(t *testing.T) {
	t.Parallel()

	rest := map[string]any{
		"module": "m", "sources": []any{"a.c"}, "pkg_config": []any{"openssl >= 3"},
		"platforms": map[string]any{runtime.GOOS: map[string]any{"pkg_config": []any{"zlib"}}},
	}

	for _, static := range []bool{false, true} {
		harness, bld := fakeHarness(t, C(), rest,
			map[string]string{"CC": "fakecc", "PKG_CONFIG": "fakepkg --quiet"})

		writeTool(t, harness, "fakepkg", fakePkgConfig)

		harness.Context.Static = static

		require.NoError(t, harness.Run(t.Context(), bld))

		object := filepath.Join(harness.Context.Dirs.Work, "obj", objectName("a.c"))
		out := filepath.Join(harness.Context.Dirs.Modules, "m"+harness.Context.Target.ModuleExt())
		libFlags := "-shared -Wl,-Bsymbolic-functions"

		if runtime.GOOS == "darwin" {
			libFlags = "-bundle -undefined dynamic_lookup -all_load"
		}

		staticFlag, libs := "", "-lssl"

		var probes []string

		if static {
			// The static libraries are looked for, and fakecc finds none.
			staticFlag, libs = " --static", "-lssl -lcrypto -ldl"
			probes = []string{"fakecc -print-file-name=libssl.a",
				"fakecc -print-file-name=libcrypto.a"}
		}

		ran := calls(t, harness)
		assert.Equal(t, slices.Concat([]string{
			"fakepkg --quiet --exists --print-errors openssl >= 3",
			"fakepkg --quiet --exists --print-errors zlib",
			"fakepkg --quiet --cflags" + staticFlag + " openssl >= 3 zlib",
			"fakepkg --quiet --libs" + staticFlag + " openssl >= 3 zlib",
		}, probes, []string{
			"fakecc -O2 -g -fPIC " + reproducible(harness) +
				" -I/tt/include/tarantool -I/opt/ssl/include -DNAME=a b -MD -MF " +
				object + ".d -MQ " + object + " -c a.c -o " + object + ".tmp",
			"fakecc " + libFlags + " -o " + out + " " + object + " " + libs,
		}), ran, "static = %t", static)
	}

	// The variables pkg-config reads are of the keys: another
	// PKG_CONFIG_PATH compiles and links again, though pkg-config gives the
	// same flags.
	harness, bld := fakeHarness(t, C(), rest,
		map[string]string{"CC": "fakecc", "PKG_CONFIG": "fakepkg"})

	writeTool(t, harness, "fakepkg", fakePkgConfig)
	writeFile(t, harness, "a.c", "/* a.c */\n")

	ranSince := func() []string {
		before := len(calls(t, harness))

		require.NoError(t, harness.Run(t.Context(), bld), harness.Printed())

		var ran []string

		for _, call := range calls(t, harness)[before:] {
			if !strings.HasPrefix(call, "fakepkg ") {
				ran = append(ran, call)
			}
		}

		return ran
	}

	assert.Len(t, ranSince(), 2, "the compile and the link")
	assert.Empty(t, ranSince(), "nothing changed")

	harness.Context.Env["PKG_CONFIG_PATH"] = "/opt/ssl/lib/pkgconfig"

	assert.Len(t, ranSince(), 2, "PKG_CONFIG_PATH changed")

	missing := map[string]any{
		"module": "m", "sources": []any{"a.c"}, "pkg_config": []any{"zlib", "nosuchlib"},
	}

	harness, bld = fakeHarness(t, C(), missing,
		map[string]string{"CC": "fakecc", "PKG_CONFIG": "fakepkg"})

	writeTool(t, harness, "fakepkg", fakePkgConfig)

	err := bld.Preflight(t.Context(), harness.Context)

	var fieldErr *build.FieldError

	require.ErrorAs(t, err, &fieldErr)
	assert.Equal(t, "pkg_config[1]", fieldErr.Field)
	assert.Contains(t, fieldErr.Msg, `"nosuchlib": fakepkg --exists --print-errors nosuchlib: `+
		"exit status 1: Package 'nosuchlib' was not found")

	harness, bld = fakeHarness(t, C(), missing,
		map[string]string{"CC": "fakecc", "PKG_CONFIG": "nosuch-pkg-config"})

	err = bld.Preflight(t.Context(), harness.Context)
	require.ErrorIs(t, err, build.ErrNoTool)
	assert.Contains(t, err.Error(), `pkg_config lists "zlib" and "nosuchlib", which the `+
		`build asks "nosuch-pkg-config" about, and it is not in PATH`)
}

// TestRunLink checks that a build links the libraries the components of link
// made for linking, from the directory of each: -I of its include/ in every
// compile, -L of the directory and -l of each library in it after
// libraries; that the build names the component Links; and that a component
// not built, or one that made no library, fails the build naming it.
func TestRunLink(t *testing.T) {
	t.Parallel()

	rest := map[string]any{
		"module": "m", "sources": []any{"a.c"}, "libraries": []any{"z"},
		"link": []any{"foo", "bar"},
	}

	harness, bld := fakeHarness(t, C(), rest, map[string]string{"CC": "fakecc"})

	writeFile(t, harness, "a.c", "/* a.c */\n")
	assert.Equal(t, []string{"foo", "bar"}, bld.Links())

	foo := harness.Context.Dirs.LinkOf("foo")
	bar := harness.Context.Dirs.LinkOf("bar")

	err := harness.Run(t.Context(), bld)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `link[0]: component "foo" has not been built: build it `+
		`first, as a build of the whole product does before "native"`)

	for _, file := range []string{
		filepath.Join(foo, "libfoo.a"), filepath.Join(foo, "libfoo.so"),
		filepath.Join(foo, "libfoo.so.1"), filepath.Join(foo, "include", "foo.h"),
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o700))
		require.NoError(t, os.WriteFile(file, nil, 0o600))
	}

	require.NoError(t, os.MkdirAll(bar, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(bar, "bar.h"), nil, 0o600))

	err = harness.Run(t.Context(), bld)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `link[1]: component "bar" made no library to link, `+
		`lib<name>.a, .so or .dylib: its output sends one to = "link"`)

	require.NoError(t, os.WriteFile(filepath.Join(bar, "libbar.a"), nil, 0o600))
	require.NoError(t, harness.Run(t.Context(), bld))

	object := filepath.Join(harness.Context.Dirs.Work, "obj", objectName("a.c"))
	out := filepath.Join(harness.Context.Dirs.Modules, "m"+harness.Context.Target.ModuleExt())
	libFlags := "-shared -Wl,-Bsymbolic-functions"

	if runtime.GOOS == "darwin" {
		libFlags = "-bundle -undefined dynamic_lookup -all_load"
	}

	assert.Equal(t, []string{
		"fakecc -O2 -g -fPIC " + reproducible(harness) + " -I/tt/include/tarantool -I" +
			filepath.Join(foo, "include") +
			" -MD -MF " + object + ".d -MQ " + object + " -c a.c -o " + object + ".tmp",
		"fakecc " + libFlags + " -o " + out + " " + object + " -L" + foo + " -L" + bar +
			" -lz -lfoo -lbar",
	}, calls(t, harness))

	// A library of link changed links the module again.
	ranBefore := len(calls(t, harness))

	require.NoError(t, harness.Run(t.Context(), bld))
	assert.Len(t, calls(t, harness), ranBefore, "nothing changed")

	require.NoError(t, os.WriteFile(filepath.Join(bar, "libbar.a"), []byte("new"), 0o600))
	require.NoError(t, harness.Run(t.Context(), bld))
	assert.Len(t, calls(t, harness), ranBefore+1, "the link again")
}
