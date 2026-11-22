package cc

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// exportOf returns the export of the build of the block rest of backend for
// component(), as the pack asks for it.
func exportOf(t *testing.T, backend build.Backend, rest map[string]any) build.Export {
	t.Helper()

	harness := buildtest.New(t, backend, component())

	bld, err := buildtest.Validate(backend, component(), block(rest, nil))
	require.NoError(t, err)

	export, err := harness.Export(bld)
	require.NoError(t, err)

	return export
}

// TestExportScript pins the script of the least module: the check of the
// headers and of the compiler, a function that compiles a C source with the
// flags of Run, CFLAGS of LuaRocks in place of -O2 -g, one that links the
// module with LIBFLAG and TT_MODULE_LDFLAGS, a call of each, and no tool for
// the core to look for.
func TestExportScript(t *testing.T) {
	t.Parallel()

	export := exportOf(t, LuaC(), map[string]any{
		"module": "fnv", "sources": []any{"fnv.c"}, "cflags": []any{"-std=gnu99", "-Wall"},
	})

	assert.Equal(t, `# The module fnv of the tt backend "lua-c": each source compiled on its
# own into an object of TT_BUILD_DIR, and the objects linked, as tt builds
# it, with the toolchain of LuaRocks. Globs are off, so that a variable of
# flags splits into its words alone.
set -f
# cc_fail message: fails the build of the component, saying why.
cc_fail() {
	printf 'the component "%s": %s\n' "$TT_COMPONENT_NAME" "$*" >&2
	exit 1
}
# cc_run command...: runs the command, printing it first, as LuaRocks
# prints the commands of a build.
cc_run() {
	printf '%s\n' "$*"
	"$@"
}
# cc_need variable value: fails the build unless the program of value, the
# first word of the variable, is in PATH.
cc_need() {
	cc_value=$2
	set -- "$1" $2
	command -v "${2-}" >/dev/null 2>&1 ||
		cc_fail "$1=$cc_value: ${2-} is not in PATH"
}
[ -n "${TT_TARANTOOL_INCLUDE_DIR-}" ] || cc_fail "no headers of Tarantool to" \
	"compile against: LUA_INCDIR of LuaRocks names no directory of module.h"
cc_need CC "$CC"
# cc_compile_c source object: compiles the C source into the object.
cc_compile_c() {
	cc_run $CC -fPIC '-std=gnu99' -Wall $CFLAGS \
		-I"$TT_TARANTOOL_INCLUDE_DIR" -c "$1" -o "$2"
}
# cc_link object...: links the objects into the module.
cc_link() {
	cc_run $CC $LIBFLAG $TT_MODULE_LDFLAGS \
		-o "$TT_OUTPUT_DIR"/fnv"$TT_MODULE_EXT" "$@" ${LDFLAGS-}
}
cc_compile_c fnv.c "$TT_BUILD_DIR"/fnv.c.040409a9.o
cc_link "$TT_BUILD_DIR"/fnv.c.040409a9.o
`, export.Script)
	assert.Empty(t, export.Tools, "the compilers are the machine's, checked by the script")
}

// TestExportScriptParts: what the script has of the fields of the block - a
// case on TT_PLATFORM_OS with an arm for each OS of platforms.<os> and one
// for the others, the C++ compiler and its flags, pkg-config, the libraries
// of link and the directory of the module in rpath.
func TestExportScriptParts(t *testing.T) {
	t.Parallel()

	script := exportOf(t, LuaC(), map[string]any{
		"module": "m", "sources": []any{"a.c", "b.cpp"}, "pkg_config": []any{"libsasl2 >= 2.1"},
		"link": []any{"fnv-lib"}, "rpath": []any{"${ORIGIN}/lib"},
		"platforms": map[string]any{"linux": map[string]any{"libraries": []any{"rt"}}},
	}).Script

	for _, part := range []string{
		"cc_cxx=${CXX:-c++} cc_cxxflags=${CXXFLAGS-'-O2'}\n",
		"cc_need CC \"$CC\"\ncc_need CXX \"$cc_cxx\"\n",
		"case $TT_PLATFORM_OS in\ndarwin) cc_origin=@loader_path ;;\n*) cc_origin='$ORIGIN' ;;\n" +
			"esac\n",
		"[ ! -d \"$TT_LINK_ROOT\"/fnv-lib/include ] || cc_include_fnv_lib=\n",
		"case $TT_PLATFORM_OS in\nlinux)\n\tcc_need PKG_CONFIG \"$cc_pkg_config\"\n" +
			"\tcc_pkg_cflags=`$cc_pkg_config --cflags 'libsasl2 >= 2.1'`\n",
		"\tcc_listed=' rt ' cc_link_libs=\n\tcc_link_libraries fnv-lib\n",
		"\t;;\n*)\n",
		"\tcc_listed=' ' cc_link_libs=\n",
		"${cc_include_fnv_lib+-I\"$TT_LINK_ROOT\"/fnv-lib/include}",
		"\t\tcc_run $cc_cxx -fPIC $cc_cxxflags \\\n\t\t\t-I\"$TT_TARANTOOL_INCLUDE_DIR\" \\\n",
		"-L\"$TT_LINK_ROOT\"/fnv-lib -lrt $cc_link_libs \\\n",
		"$cc_pkg_libs -Wl,-rpath,\"$cc_origin\"/lib ${LDFLAGS-}\n",
		"cc_compile_cxx b.cpp \"$TT_BUILD_DIR\"/b.cpp.",
	} {
		assert.Contains(t, script, part)
	}

	assert.Equal(t, 2, strings.Count(script, "cc_link() {"), "one link of each arm:\n%s", script)
}

// TestExportLost: what the script lacks against Run, of either backend and
// of the languages of the sources.
func TestExportLost(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{
		"the cache of its objects and compile_commands.json",
		"the checks of the module it links: of its entry point, of the symbols it exports " +
			"that Tarantool exports too, and of its load into Tarantool",
		"-g of the default -O2 -g: C compiles with CFLAGS of LuaRocks, -O2 -fPIC by default, " +
			"C++ with CXXFLAGS, -O2 by default",
		"the flags that keep the paths of the project out of the objects",
	}, exportOf(t, LuaC(), module(map[string]any{"sources": []any{"a.c", "b.cc"}})).Lost)

	lost := exportOf(t, C(), module(map[string]any{"sources": []any{"b.cc"}})).Lost
	require.Len(t, lost, 4)
	assert.Equal(t, "the checks of the module it links: of the symbols it exports that "+
		"Tarantool exports too, and of its load into Tarantool", lost[1], "c has no entry point")
	assert.Equal(t, "-g of the default -O2 -g: C++ compiles with CXXFLAGS, -O2 by default",
		lost[2])
}

// TestExportOfTtAlone: a build of static = true and one of bundle are made
// with tt alone, naming the field.
func TestExportOfTtAlone(t *testing.T) {
	t.Parallel()

	for field, test := range map[string]struct {
		block buildtest.Block
		msg   string
	}{
		"static": {
			buildtest.Block{
				Common:      build.Common{Cwd: "", Env: nil, Output: nil, Static: true},
				Incremental: false, Fields: module(nil),
			},
			"tt links a module of static = true of the static library it finds of each " +
				"library, and checks that it needs no library of the machine, which a build " +
				"without tt does not",
		},
		"bundle": {
			block(module(map[string]any{"libraries": []any{"z"}, "bundle": []any{"z"}}), nil),
			"tt copies the shared libraries of bundle beside the module, which a build " +
				"without tt does not",
		},
	} {
		bld, err := buildtest.Validate(LuaC(), component(), test.block)
		require.NoError(t, err, field)

		_, err = buildtest.New(t, LuaC(), component()).Export(bld)
		require.ErrorIs(t, err, build.ErrNoExport, field)

		var fieldErr *build.FieldError

		require.ErrorAs(t, err, &fieldErr, field)
		assert.Equal(t, field, fieldErr.Field)
		assert.Equal(t, test.msg, fieldErr.Msg)
	}
}

// loggingCompiler is a fake compiler: it writes each of its arguments on a
// line of $FAKE_LOG, then a line --, and makes the file of its -o.
const loggingCompiler = `#!/bin/sh
{ for arg; do printf '%s\n' "$arg"; done; echo --; } >> "$FAKE_LOG"
out= prev=
for arg; do [ "$prev" != -o ] || out=$arg; prev=$arg; done
[ -z "$out" ] || : > "$out"
`

// loggingPkgConfig is a fake pkg-config of the flags of zlib, which records
// how it was run as loggingCompiler does.
const loggingPkgConfig = `#!/bin/sh
{ for arg; do printf '%s\n' "$arg"; done; echo --; } >> "$FAKE_LOG"
case $1 in
--cflags) echo '-I/opt/z/include -DZ_SOLO' ;;
--libs) echo '-L/opt/z/lib -lz' ;;
esac
`

// TestExportRuns runs the script of a build of every field, for Linux and
// for macOS, with fake compilers and a fake pkg-config that record how they
// are run, and checks their arguments, each a word as the block has it: the
// arm of the OS of TT_PLATFORM_OS, the compiler of each language, the flags
// of each, the libraries a component of link made, but those libraries
// names, and the directory of the module in rpath as the loader of the OS
// names it.
func TestExportRuns(t *testing.T) {
	t.Parallel()

	for _, goos := range build.SupportedOS() {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()

			harness := buildtest.New(t, LuaC(), component())
			harness.SetTarget(build.Target{OS: goos, Arch: "amd64"})

			prefix := t.TempDir()
			include := filepath.Join(prefix, "include", "tarantool")
			require.NoError(t, os.MkdirAll(include, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(include, "module.h"), nil, 0o600))
			harness.SetTarantool(build.Tarantool{
				Prefix: prefix, IncludeDir: include, Version: "", Executable: "",
			})

			// The compilers are one program run with a word of its own each,
			// as CC="ccache cc" is.
			tools := t.TempDir()
			fake, pkgConfig := filepath.Join(tools, "fakecc"), filepath.Join(tools, "pkg-config")
			log := filepath.Join(tools, "log")

			writeProgram(t, fake, loggingCompiler)
			writeProgram(t, pkgConfig, loggingPkgConfig)

			maps.Copy(harness.Context.Env, map[string]string{
				"CC": fake + " --cc", "CXX": fake + " --cxx",
				"CFLAGS": "-O2 -fPIC", "CXXFLAGS": "-O3", "LDFLAGS": "-Wl,-z,now",
				"PKG_CONFIG": pkgConfig, "FAKE_LOG": log,
			})

			linked := harness.Context.Dirs.LinkOf("fnvlib")
			for _, dir := range []string{filepath.Join(linked, "include"), filepath.Join(linked,
				"libdir.a")} {
				require.NoError(t, os.MkdirAll(dir, 0o700))
			}

			for _, file := range []string{"libfnv.a", "libfnv.so", "libz.a", "notes.txt"} {
				require.NoError(t, os.WriteFile(filepath.Join(linked, file), nil, 0o600))
			}

			bld, err := buildtest.Validate(LuaC(), component(), block(map[string]any{
				"module": "m", "sources": []any{"a.c", "sub/b.cpp"},
				"cflags": []any{"-Wall"}, "cxxflags": []any{"-std=c++17"},
				"defines":      []any{"A=1", `MSG="it's $HOME"`},
				"include_dirs": []any{"inc", "sp ace"},
				"libraries":    []any{"z", "vendor/libq.a"}, "library_dirs": []any{"vendor"},
				"rpath": []any{"$ORIGIN/lib"}, "ldflags": []any{"-pthread"},
				"pkg_config": []any{"zlib"}, "link": []any{"fnvlib"},
				"platforms": map[string]any{
					"darwin": map[string]any{
						"defines": []any{"ON_DARWIN"}, "libraries": []any{"c++"},
					},
					"linux": map[string]any{"ldflags": []any{"-Wl,--as-needed"}},
				},
			}, nil))
			require.NoError(t, err)

			export, err := harness.Export(bld)
			require.NoError(t, err)
			require.NoError(t, harness.RunExport(t.Context(), export), harness.Printed())

			data, err := os.ReadFile(log)
			require.NoError(t, err)

			dirs := harness.Context.Dirs
			assert.Equal(t, exportRunCalls(goos, dirs, include), string(data))
			assert.FileExists(t, filepath.Join(dirs.Modules, "m"+build.Target{
				OS: goos, Arch: "amd64",
			}.ModuleExt()))
		})
	}
}

// exportRunCalls are the runs of the fake tools TestExportRuns expects of
// the script for goos, in dirs, the headers of Tarantool in include, as
// loggingCompiler records them.
func exportRunCalls(goos string, dirs build.Dirs, include string) string {
	objects := []string{
		filepath.Join(dirs.Work, objectName("a.c")),
		filepath.Join(dirs.Work, objectName("sub/b.cpp")),
	}
	linked := dirs.LinkOf("fnvlib")
	// The arm of Linux and the toolchain of the script on Linux.
	define, libFlag := []string(nil), []string{"-shared", "-Wl,-Bsymbolic-functions"}
	ldflags, library := []string{"-Wl,--as-needed"}, []string(nil)
	module, origin := filepath.Join(dirs.Modules, "m.so"), "$ORIGIN"

	if goos == "darwin" {
		define = []string{"-DON_DARWIN"}
		libFlag = []string{"-bundle", "-undefined", "dynamic_lookup", "-all_load"}
		ldflags, library = nil, []string{"-lc++"}
		module, origin = filepath.Join(dirs.Modules, "m.dylib"), "@loader_path"
	}

	headers := slices.Concat([]string{"-I" + include, "-DA=1", `-DMSG="it's $HOME"`}, define,
		[]string{
			"-Iinc", "-Isp ace", "-I" + filepath.Join(linked, "include"), "-I/opt/z/include",
			"-DZ_SOLO",
		})

	var calls strings.Builder

	for _, call := range [][]string{
		{"--cflags", "zlib"},
		{"--libs", "zlib"},
		slices.Concat([]string{"--cc", "-fPIC", "-Wall", "-O2", "-fPIC"}, headers,
			[]string{"-c", "a.c", "-o", objects[0]}),
		slices.Concat([]string{"--cxx", "-fPIC", "-std=c++17", "-O3"}, headers,
			[]string{"-c", "sub/b.cpp", "-o", objects[1]}),
		slices.Concat([]string{"--cxx"}, libFlag, []string{"-pthread"}, ldflags,
			[]string{"-o", module}, objects, []string{"-Lvendor", "-L" + linked, "-lz",
				"vendor/libq.a"}, library, []string{
				"-lfnv", "-L/opt/z/lib", "-lz", "-Wl,-rpath," + origin + "/lib", "-Wl,-z,now",
			}),
	} {
		calls.WriteString(strings.Join(call, "\n") + "\n--\n")
	}

	return calls.String()
}

// TestExportFails: the script fails, saying why, without the headers of
// Tarantool, with a compiler that is not in PATH, and with a component of
// link that made nothing to link.
func TestExportFails(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		rest  map[string]any
		env   map[string]string
		setup func(*buildtest.Harness)
		want  string
	}{
		{
			name: "no headers", rest: module(nil), env: nil, setup: nil,
			want: `the component "native": no headers of Tarantool to compile against: ` +
				"LUA_INCDIR of LuaRocks names no directory of module.h",
		},
		{
			name: "no compiler", rest: module(nil), env: map[string]string{
				"CC": "/nonexistent/cc -x",
			},
			setup: withHeaders,
			want:  `the component "native": CC=/nonexistent/cc -x: /nonexistent/cc is not in PATH`,
		},
		{
			name: "nothing linked", rest: module(map[string]any{"link": []any{"fnvlib"}}),
			env: map[string]string{"CC": "true"},
			setup: func(harness *buildtest.Harness) {
				withHeaders(harness)
				require.NoError(t, os.MkdirAll(harness.Context.Dirs.LinkOf("fnvlib"), 0o700))
			},
			want: `the component "native": the component fnvlib of link made no library to ` +
				`link, lib<name>.a, .so or .dylib: its output sends one to = "link"`,
		},
		{
			name: "not built", rest: module(map[string]any{"link": []any{"fnvlib"}}),
			env: map[string]string{"CC": "true"}, setup: withHeaders,
			want: `the component "native": the component fnvlib of link has not been built`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness := buildtest.New(t, LuaC(), component())

			maps.Copy(harness.Context.Env, test.env)

			if test.setup != nil {
				test.setup(harness)
			}

			bld, err := buildtest.Validate(LuaC(), component(), block(test.rest, nil))
			require.NoError(t, err)

			export, err := harness.Export(bld)
			require.NoError(t, err)
			require.Error(t, harness.RunExport(t.Context(), export))
			assert.Equal(t, test.want+"\n", harness.Printed())
		})
	}
}

// withHeaders tells harness a Tarantool of an empty module.h.
func withHeaders(harness *buildtest.Harness) {
	include := filepath.Join(harness.Context.Dirs.State, "include", "tarantool")

	_ = os.MkdirAll(include, 0o700)
	_ = os.WriteFile(filepath.Join(include, "module.h"), nil, 0o600)

	harness.SetTarantool(build.Tarantool{
		Prefix: filepath.Dir(filepath.Dir(include)), IncludeDir: include, Version: "",
		Executable: "",
	})
}
