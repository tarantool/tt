package pack

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luarocks/client"

	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// cManifest is a package of a module of lua-c, cdemo.fast, of two sources
// and a define of a quote, which links the library and includes the header
// a component of make makes for linking, and of its Lua module, cdemo. Its
// repository is the %s.
const cManifest = `manifest_version = '0.1'

[package]
name = 'cdemo'
license = 'BSD-2-Clause'
license_files = ['LICENSE']
repository = '%s'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[products.default]
components = ['fnvlib', 'cdemo']

[components.fnvlib]
path = 'fnvlib'

[components.fnvlib.build]
backend = 'make'
make_target = 'lib'
output = [{ path = 'libfnv.a', to = 'link' }, { path = 'fnv.h', to = 'link' }]

[components.cdemo]
path = 'cdemo'

[components.cdemo.build]
backend = 'lua-c'
module = 'fast'
sources = ['fast.c', 'util/offset.c']
defines = ["GREETING=\"it's\""]
link = ['fnvlib']
`

// cFastC is the entry point of cdemo.fast, which declares the one function
// of Lua it calls itself, the headers of Tarantool of the tests being empty.
const cFastC = `#include "fnv.h"

typedef struct lua_State lua_State;
extern void lua_pushinteger(lua_State *L, long n);
int offset(void);

static const char greeting[] = GREETING;

int luaopen_cdemo_fast(lua_State *L)
{
	lua_pushinteger(L, fnv_answer() + offset() + (long)sizeof(greeting));
	return 1;
}
`

// cFnvMakefile is fnvlib/Makefile of cManifest: the static library, built
// with -fPIC, of the toolchain of LuaRocks or its own.
const cFnvMakefile = `CC ?= cc
CFLAGS ?= -O2

lib:
	$(CC) $(CFLAGS) -fPIC -c fnv.c -o $(TT_BUILD_DIR)/fnv.o
	ar rcs libfnv.a $(TT_BUILD_DIR)/fnv.o
`

// writeCPackage writes the project of cManifest as a git repository whose
// HEAD is the tag v1.2.0, the repository of the package at its git+file
// URL, and returns it with a prefix of the headers of Tarantool, an empty
// module.h, which the pack builds the module against. git ignores what its
// builds make.
func writeCPackage(t *testing.T) (string, string) {
	t.Helper()

	for _, tool := range []string{"cc", "make", "ar"} {
		_, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("no %s", tool)
		}
	}

	src := t.TempDir()
	writeTree(t, src, map[string]string{
		manifestFileName:      fmt.Sprintf(cManifest, "git+file://"+src),
		".gitignore":          pureGitignore + "/fnvlib/libfnv.a\n/.tt-rock/\n",
		"LICENSE":             "BSD-2-Clause text\n",
		"cdemo/init.lua":      "return { answer = require('cdemo.fast') }\n",
		"cdemo/fast.c":        cFastC,
		"cdemo/util/offset.c": "int offset(void) { return 0; }\n",
		"fnvlib/Makefile":     cFnvMakefile,
		"fnvlib/fnv.h":        "int fnv_answer(void);\n",
		"fnvlib/fnv.c":        "int fnv_answer(void) { return 42; }\n",
	})

	git(t, src, "init", "-q")
	git(t, src, "add", "-A")
	git(t, src, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	git(t, src, "tag", "v1.2.0")

	prefix := t.TempDir()
	writeTree(t, prefix, map[string]string{"include/tarantool/module.h": ""})

	return src, prefix
}

// TestSrcRockOfCBuildsWithLuaRocks: LuaRocks with no tt - go-luarocks
// natively, and running the LuaRocks 3.9.2 that tt rocks of tt 2 is -
// builds the source rock of a package of a module of lua-c, and its bare
// rockspec, by the script the rockspec carries: the compiles and the link
// of the export of lua-c, with the toolchain of LuaRocks, against the
// library and the header a component of make made for linking, into the
// files of the binary rock.
func TestSrcRockOfCBuildsWithLuaRocks(t *testing.T) {
	t.Parallel()

	src, prefix := writeCPackage(t)

	var warnings []string

	opts := packOptions(src, filepath.Join(t.TempDir(), "out"), FormatRock, FormatRockSrc)

	opts.Build.Tarantool = rocks.TarantoolInfo{Prefix: prefix, Version: "3.0.0"}
	opts.Warn = func(msg string) { warnings = append(warnings, msg) }

	result, err := Run(context.Background(), opts)
	require.NoError(t, err)
	require.Len(t, warnings, 1, "what the script lacks against tt: %q", warnings)
	assert.Contains(t, warnings[0], `the component "cdemo": the cache of its objects`)

	table := buildTable(t, result.Rockspec)
	assert.Contains(t, table, `   type = "command",`)
	assert.Contains(t, table, "\ttt_output libfnv.a \"$TT_LINK_DIR\"/libfnv.a\n")
	assert.Contains(t, table, "\ncc_link_libraries fnvlib\n")
	assert.Contains(t, table, `'-DGREETING="it'\''s"'`)

	// The module is a .dylib on macOS, as Tarantool loads it there.
	module := "fast.so"
	if runtime.GOOS == "darwin" {
		module = "fast.dylib"
	}

	entries := zipEntries(t, result.Rock)
	assert.Contains(t, entries, "lib/cdemo/"+module)

	// The binary rock is installed by the native backend alone, as
	// TestSrcRockOfBuildsBuildsWithLuaRocks says why.
	binary := luaRocksInstall(t, client.BackendNative, result.Rock)
	require.NoError(t, binary.err)

	want := installedRock(t, binary.tree, result.Rockspec)
	require.Contains(t, want.Lib, "cdemo/"+module)

	for name, backend := range map[string]client.Backend{
		"native": client.BackendNative, "lua": client.BackendLua,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srcRock := luaRocksInstall(t, backend, result.SrcRock)
			require.NoError(t, srcRock.err)

			bare := luaRocksBuild(t, backend, result.Rockspec)
			require.NoError(t, bare.err)

			for what, tree := range map[string]string{
				"source rock": srcRock.tree, "bare rockspec": bare.tree,
			} {
				assertSameRock(t, want, installedRock(t, tree, result.Rockspec), what)

				built, err := os.ReadFile(filepath.Join(tree, "lib", "tarantool", "cdemo",
					module))
				require.NoError(t, err, what)
				assert.Contains(t, string(built), "luaopen_cdemo_fast", what)
			}
		})
	}
}
