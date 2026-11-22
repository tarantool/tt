package build_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/build/buildtest"
)

// The cases of c and lua-c compile against the headers of Tarantool, as the
// tests of the backend do (README.md): with the build tag integration, the
// headers under the prefix TT_CLI_TARANTOOL_PREFIX names, or under that of
// the tarantool in PATH.

// tarantoolPrefixEnv names the prefix of the headers of Tarantool of the
// tests, as it names it for tt.
const tarantoolPrefixEnv = "TT_CLI_TARANTOOL_PREFIX"

// withTarantool tells harness the Tarantool of the build, one of the headers
// of tarantoolPrefixEnv, else of the prefix of the tarantool in PATH, and
// skips the test without the build tag integration, without a C compiler,
// or without module.h of the prefix. It has no executable, so Run loads the
// module into none.
func withTarantool(t *testing.T, harness *buildtest.Harness) {
	t.Helper()

	if !integrationTag {
		t.Skip("the case compiles against the headers of Tarantool: run it with -tags integration")
	}

	requireTool(t, toolOf("CC", "cc"))

	prefix := os.Getenv(tarantoolPrefixEnv)
	if prefix == "" {
		tarantool, err := exec.LookPath("tarantool")
		if err != nil {
			t.Skipf("no headers of Tarantool: %s is not set, and no tarantool is in PATH",
				tarantoolPrefixEnv)
		}

		tarantool, err = filepath.EvalSymlinks(tarantool)
		require.NoError(t, err)

		prefix = filepath.Dir(filepath.Dir(tarantool))
	}

	include := filepath.Join(prefix, "include", "tarantool")

	_, err := os.Stat(filepath.Join(include, "module.h"))
	if err != nil {
		t.Skipf("no headers of Tarantool in %s: %v; set %s to a prefix of them", include, err,
			tarantoolPrefixEnv)
	}

	harness.SetTarantool(sdkbuild.Tarantool{
		Prefix: prefix, IncludeDir: include, Version: "", Executable: "",
	})
}

// toolOf is the words of the program the variable key of the environment
// names, split on blanks as the script splits it, those of fallback when it
// is unset or empty.
func toolOf(key, fallback string) []string {
	words := strings.Fields(os.Getenv(key))
	if len(words) == 0 {
		return []string{fallback}
	}

	return words
}

// requireTool skips the test unless the program of words, the first, is in
// PATH.
func requireTool(t *testing.T, words []string) {
	t.Helper()

	_, err := exec.LookPath(words[0])
	if err != nil {
		t.Skipf("%s is not in PATH: %v", words[0], err)
	}
}

// run runs argv in dir, failing the test when it fails.
func run(t *testing.T, dir string, argv ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), argv[0], argv[1:]...)

	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%v: %s", argv, out)
}

// withFnvLibrary is withTarantool, and the library of the component fnvlib
// for linking, in its directory (sdkbuild.Dirs.LinkOf), as its build would
// have made it before the build of the case: libfnv.a, of fnv_answer, built
// with -fPIC, and its header, include/fnv.h. It skips the test without ar.
func withFnvLibrary(t *testing.T, harness *buildtest.Harness) {
	t.Helper()

	withTarantool(t, harness)
	requireTool(t, []string{"ar"})

	dir := harness.Context.Dirs.LinkOf("fnvlib")
	source := filepath.Join(t.TempDir(), "fnv.c")

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "include"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "include", "fnv.h"),
		[]byte("int fnv_answer(void);\n"), 0o600))
	require.NoError(t, os.WriteFile(source, []byte("int fnv_answer(void) { return 42; }\n"),
		0o600))

	run(t, dir, slices.Concat(toolOf("CC", "cc"), []string{"-fPIC", "-c", source, "-o",
		"fnv.o"})...)
	run(t, dir, "ar", "rcs", "libfnv.a", "fnv.o")
	require.NoError(t, os.Remove(filepath.Join(dir, "fnv.o")))
}

// luaEntry is a source of C of the entry point of the lua-c module demo.<name>,
// which returns what the C expression value is.
func luaEntry(name, value string) string {
	return "#include <lua.h>\n\n" + "int luaopen_demo_" + name + "(lua_State *L)\n{\n" +
		"\tlua_pushinteger(L, " + value + ");\n\treturn 1;\n}\n"
}

// ccExportCases are the cases of c and lua-c of exportCases: a module of two
// sources, of C and C++, of the macros and the directories of headers of
// the block, of the lists of platforms.<os>, of a package of pkg-config and
// of the library of a component of link. What the script lacks against Run
// is the cache and the checks of the module, which none of them needs.
func ccExportCases() []exportCase {
	return []exportCase{
		{
			name: "lua-c of two sources", backend: "lua-c",
			fields: map[string]any{"module": "fast", "sources": []any{"fast.c", "util.c"}},
			files: map[string]string{
				"fast.c": "int util_answer(void);\n" + luaEntry("fast", "util_answer()"),
				"util.c": "int util_answer(void) { return 42; }\n",
			},
			setup: withTarantool, lost: true,
		},
		{
			// The module is linked by the C++ compiler.
			name: "lua-c of C and C++", backend: "lua-c",
			fields: map[string]any{
				"module": "mixed", "sources": []any{"entry.c", "ring/ring.cpp"},
				"cflags": []any{"-std=gnu99"}, "cxxflags": []any{"-std=c++11"},
			},
			files: map[string]string{
				"entry.c": "int ring_size(void);\n" + luaEntry("mixed", "ring_size()"),
				"ring/ring.cpp": "#include <vector>\n\n" +
					"extern \"C\" int ring_size(void)\n{\n\tstd::vector<int> ring(3);\n" +
					"\treturn (int)ring.size();\n}\n",
			},
			setup: func(t *testing.T, harness *buildtest.Harness) {
				t.Helper()

				withTarantool(t, harness)
				requireTool(t, toolOf("CXX", "c++"))
			},
			lost: true,
		},
		{
			// A define of a quote and blanks is one word, and so is a
			// directory of a blank.
			name: "lua-c of defines and include_dirs", backend: "lua-c",
			fields: map[string]any{
				"module": "defs", "sources": []any{"src/defs.c"},
				"defines":      []any{"ANSWER=40", `GREETING="it's a $HOME"`},
				"include_dirs": []any{"include", "more headers"},
			},
			files: map[string]string{
				"include/offset.h":        "#define OFFSET 2\n",
				"more headers/greeting.h": "static const char greeting[] = GREETING;\n",
				"src/defs.c": "#include \"offset.h\"\n#include \"greeting.h\"\n\n" +
					luaEntry("defs", "ANSWER + OFFSET + (int)sizeof(greeting)"),
			},
			setup: withTarantool, lost: true,
		},
		{
			// The source compiles on Linux with the macro of platforms.linux
			// alone, and elsewhere without it; rpath has the directory of
			// the module.
			name: "c of platforms", backend: "c",
			fields: map[string]any{
				"module": "plat", "sources": []any{"plat.c"}, "rpath": []any{"$ORIGIN/lib"},
				"platforms": map[string]any{
					"linux": map[string]any{
						"defines": []any{"ON_LINUX=1"}, "libraries": []any{"m"},
					},
				},
			},
			files: map[string]string{
				"plat.c": "#if defined(__linux__) != defined(ON_LINUX)\n" +
					"#error the defines of platforms.linux are for Linux alone\n#endif\n\n" +
					"int plat(void) { return 1; }\n",
			},
			setup: withTarantool, lost: true,
		},
		{
			name: "lua-c of pkg_config", backend: "lua-c",
			fields: map[string]any{
				"module": "zcrc", "sources": []any{"zcrc.c"}, "pkg_config": []any{"zlib"},
			},
			files: map[string]string{
				"zcrc.c": "#include <zlib.h>\n" +
					luaEntry("zcrc", "(lua_Integer)crc32(0L, Z_NULL, 0)"),
			},
			setup: func(t *testing.T, harness *buildtest.Harness) {
				t.Helper()

				withTarantool(t, harness)

				pkgConfig := toolOf("PKG_CONFIG", "pkg-config")
				requireTool(t, pkgConfig)

				err := exec.CommandContext(t.Context(), pkgConfig[0],
					append(pkgConfig[1:], "--exists", "zlib")...).Run()
				if err != nil {
					t.Skipf("%v knows no zlib: %v", pkgConfig, err)
				}
			},
			lost: true,
		},
		{
			// The header of fnvlib is in include/ of its directory, and its
			// libfnv.a is linked by -lfnv.
			name: "lua-c of link", backend: "lua-c",
			fields: map[string]any{
				"module": "hashes", "sources": []any{"hashes.c"}, "link": []any{"fnvlib"},
			},
			files: map[string]string{
				"hashes.c": "#include \"fnv.h\"\n" + luaEntry("hashes", "fnv_answer()"),
			},
			setup: withFnvLibrary, lost: true,
		},
	}
}
