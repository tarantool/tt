package pack

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"

	"github.com/tarantool/tt/v3/cli/manifest/build"
)

// scriptedManifest is a package of a component of Lua, one of shell that
// compiles a module and a program and lists them in its output with a data
// file of its argument - a literal of quotes, $, $(...) and `...` - one of
// make in a namespace of its own, which compiles a module into TT_OUTPUT_DIR
// and lists a data file, and a hook pre_build that writes the header the
// build of shell compiles with. Its repository is the first %s, and the
// literal the second (nativeArg).
const scriptedManifest = `manifest_version = '0.1'

[package]
name = 'nativedemo'
license = 'BSD-2-Clause'
license_files = ['LICENSE']
repository = '%s'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[components.lib]
path = 'nativedemo'

[components.native]
path = 'native'

[components.native.build]
backend = 'shell'
command = 'sh'
args = ['./build.sh', %s]
env = { ANSWER_HEADER = 'answer.h' }
output = [
   { path = 'out/fast.so', to = 'lib' },
   { path = 'out/nativetool', to = 'bin' },
   { path = 'out/arg.txt', to = 'share' },
   { path = 'out/answer.txt', to = 'share' },
]

[components.calc]
path = 'calc'
namespace = 'calcns'

[components.calc.build]
backend = 'make'
make_target = 'all'
output = [{ path = 'calc.json', to = 'share' }]

[hooks.pre_build]
backend = 'shell'
command = 'sh'
args = ['-c', 'printf "#define ANSWER 42\n" > native/answer.h']
`

// nativeArg is the literal of scriptedManifest, as a string of TOML, and the
// text it is.
const (
	nativeArg     = `"it's $HOME $(echo no) ` + "`echo no`" + ` \\ *"`
	nativeArgText = "it's $HOME $(echo no) `echo no` \\ *"
)

// nativeBuildScript is native/build.sh of scriptedManifest: it compiles
// fast.c, with the header pre_build writes, into a module of Tarantool, and
// tool.c into a program, with the toolchain of LuaRocks or its own, as a
// package that builds both ways does, and copies the header to a data file.
// tt runs no hook of a source rock it builds, so the header of the build of
// tt answers 0.
const nativeBuildScript = `#!/bin/sh
set -eu
mkdir -p out
[ -f "$ANSWER_HEADER" ] || printf '#define ANSWER 0\n' > "$ANSWER_HEADER"
cp "$ANSWER_HEADER" out/answer.txt
case ` + "`uname -s`" + ` in
Darwin) libflag='-bundle -undefined dynamic_lookup' ;;
*) libflag=-shared ;;
esac
${CC:-cc} ${CFLAGS:--O2 -fPIC} -c fast.c -o "$TT_BUILD_DIR/fast.o"
${CC:-cc} ${LIBFLAG:-$libflag} -o out/fast.so "$TT_BUILD_DIR/fast.o"
${CC:-cc} -o out/nativetool tool.c
printf '%s\n' "$1" > out/arg.txt
`

// nativeFastC is a module of Tarantool that returns ANSWER, of the header
// pre_build writes, declaring the one function of Lua it calls itself.
const nativeFastC = `#include "answer.h"

typedef struct lua_State lua_State;
extern void lua_pushinteger(lua_State *L, long n);

int luaopen_nativedemo_fast(lua_State *L)
{
	lua_pushinteger(L, ANSWER);
	return 1;
}
`

// nativeMakefile is calc/Makefile of scriptedManifest.
const nativeMakefile = `CFLAGS ?= -O2 -fPIC
LIBFLAG ?= -shared

all:
	$(CC) $(CFLAGS) -c calc.c -o $(TT_BUILD_DIR)/calc.o
	$(CC) $(LIBFLAG) -o $(TT_OUTPUT_DIR)/calc.so $(TT_BUILD_DIR)/calc.o
	printf '{"package": "%s"}\n' "$$TT_PACKAGE" > calc.json
`

// writeNativePackage writes the project of scriptedManifest as a git
// repository whose HEAD is the tag v1.2.0, the repository of the package at
// its git+file URL. git ignores what its builds make.
func writeNativePackage(t *testing.T) string {
	t.Helper()

	for _, tool := range []string{"cc", "make"} {
		_, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("no %s", tool)
		}
	}

	src := t.TempDir()
	writeTree(t, src, map[string]string{
		manifestFileName: fmt.Sprintf(scriptedManifest, "git+file://"+src, nativeArg),
		".gitignore": pureGitignore + "/native/out/\n/native/answer.h\n/calc/calc.json\n" +
			"/.tt-rock/\n",
		"LICENSE":             "BSD-2-Clause text\n",
		"README.md":           "# nativedemo\n",
		"nativedemo/init.lua": "return { fast = require('nativedemo.fast') }\n",
		"native/build.sh":     nativeBuildScript,
		"native/fast.c":       nativeFastC,
		"native/tool.c":       "int main(void) { return 0; }\n",
		"calc/Makefile":       nativeMakefile,
		"calc/calc.c":         "int luaopen_calcns_calc(void *L) { (void)L; return 0; }\n",
	})

	git(t, src, "init", "-q")
	git(t, src, "add", "-A")
	git(t, src, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	git(t, src, "tag", "v1.2.0")

	return src
}

// installedPaths are the paths of the files of section of rm, a section of a
// rock_manifest.
func installedPaths(section map[string]string) []string {
	return slices.Sorted(maps.Keys(section))
}

// assertSameRock checks that got, a rock LuaRocks built, has the files of
// want, the binary rock: lua/, lib/ and bin/ the same paths, lua/ the same
// bytes - native files are compiled again, so of other bytes - and doc/ more.
func assertSameRock(t *testing.T, want, got *luarocks.RockManifest, what string) {
	t.Helper()

	assert.Equal(t, want.Lua, got.Lua, what)
	assert.Equal(t, installedPaths(want.Lib), installedPaths(got.Lib), what)
	assert.Equal(t, installedPaths(want.Bin), installedPaths(got.Bin), what)

	for doc, sum := range want.Doc {
		assert.Equal(t, sum, got.Doc[doc], "%s: doc/%s", what, doc)
	}
}

// TestSrcRockOfBuildsBuildsWithLuaRocks: LuaRocks with no tt and no
// manifest - go-luarocks natively, and running the LuaRocks 3.9.2 that tt
// rocks of tt 2 is - builds the source rock of a package of builds of make
// and shell, and its bare rockspec from the git repository of the package,
// by the script the rockspec carries, into the files of its binary rock: the
// module and the program of shell, its data file of the literal as it is in
// the manifest, the module of make, the module the layout of tt takes of what
// shell left in its component, and Lua, _version.lua among it. tt builds the
// source rock from its manifest as ever.
func TestSrcRockOfBuildsBuildsWithLuaRocks(t *testing.T) {
	t.Parallel()

	src := writeNativePackage(t)

	var warnings []string

	opts := packOptions(src, filepath.Join(t.TempDir(), "out"), FormatRock, FormatRockSrc)

	opts.Warn = func(msg string) { warnings = append(warnings, msg) }

	result, err := Run(context.Background(), opts)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	table := buildTable(t, result.Rockspec)
	assert.Contains(t, table, `   type = "command",`)
	assert.Contains(t, table, `["nativedemo.init"] = "nativedemo/init.lua",`)
	assert.Contains(t, table, `["nativedemo._version"] = "tt-rock-version.lua",`)
	assert.Contains(t, table, "\ttt_lay native/out/fast.so lib/nativedemo/out/fast.so\n")
	assert.NotContains(t, table, `= "native/`, "build.install has no file a build makes")

	entries := zipEntries(t, result.Rock)
	for _, entry := range []string{
		"lib/nativedemo/fast.so", "lib/nativedemo/out/fast.so", "lib/calcns/calc.so",
		"bin/nativetool", "lua/nativedemo/arg.txt", "lua/calcns/calc.json",
	} {
		assert.Contains(t, entries, entry)
	}

	assert.Equal(t, nativeArgText+"\n", entries["lua/nativedemo/arg.txt"])
	// The binary rock and the builds of LuaRocks, whose lua/ is the same, run
	// pre_build.
	assert.Equal(t, "#define ANSWER 42\n", entries["lua/nativedemo/answer.txt"])

	// The rockspec is the same packed of a clone that was never built, whose
	// layout lacks what the builds make.
	clone := filepath.Join(t.TempDir(), "clone")
	git(t, src, "clone", "-q", src, clone)

	srcOnly, err := Run(context.Background(),
		packOptions(clone, filepath.Join(t.TempDir(), "out"), FormatRockSrc))
	require.NoError(t, err)

	built, err := os.ReadFile(result.Rockspec)
	require.NoError(t, err)

	fresh, err := os.ReadFile(srcOnly.Rockspec)
	require.NoError(t, err)
	assert.Equal(t, string(built), string(fresh))

	// The binary rock is installed by the native backend alone: LuaRocks,
	// that of the lua backend, takes the platform from the first
	// architecture of /bin/sh, which on a macOS of arm64 may be x86_64, and
	// refuses a rock of macosx-aarch64 then. The source rock is what LuaRocks
	// builds on any machine.
	binary := luaRocksInstall(t, client.BackendNative, result.Rock)
	require.NoError(t, binary.err)

	want := installedRock(t, binary.tree, result.Rockspec)
	require.Contains(t, want.Lua, "nativedemo/_version.lua")
	require.Contains(t, want.Lib, "calcns/calc.so")

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
				got := installedRock(t, tree, result.Rockspec)

				assertSameRock(t, want, got, what)
				assert.Contains(t, got.Doc, "README.md", what)

				rockDir := filepath.Join(tree, "share", "tarantool", "rocks", "nativedemo",
					"1.2.0-1")
				info, err := os.Stat(filepath.Join(rockDir, "bin", "nativetool"))
				require.NoError(t, err, what)
				assert.NotZero(t, info.Mode().Perm()&0o100, "%s: the program is executable",
					what)
			}
		})
	}
}

// TestSrcRockOfBuildsBuildsWithTt: tt builds the source rock of a package
// of builds from its manifest, not by the script of its rockspec, as it does
// any source rock it packed: it runs no hook of it, as the script does.
func TestSrcRockOfBuildsBuildsWithTt(t *testing.T) {
	t.Parallel()

	src := writeNativePackage(t)
	out := filepath.Join(t.TempDir(), "server")

	result, err := Run(context.Background(), packOptions(src, out, FormatRockSrc))
	require.NoError(t, err)
	require.NotEmpty(t, result.SrcRock)

	consumer := t.TempDir()
	writeTree(t, consumer, map[string]string{
		manifestFileName: fmt.Sprintf(`manifest_version = '0.1'

[package]
name = 'consumer'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[dependencies]
nativedemo = { version = '>=1.0.0', registry = '%s' }

[components.app]
path = '.'
include = ['init.lua']
`, out),
		versionFileName: "0.1.0\n",
		"init.lua":      "return require('nativedemo')\n",
	})

	require.NoError(t, build.Run(context.Background(), consumerOptions(consumer)))

	rocksTree := filepath.Join(consumer, rocksDirName)
	for _, file := range []string{
		"lib/tarantool/nativedemo/fast.so", "lib/tarantool/calcns/calc.so",
		"share/tarantool/nativedemo/init.lua", "share/tarantool/nativedemo/arg.txt",
		"bin/nativetool",
	} {
		assert.FileExists(t, filepath.Join(rocksTree, filepath.FromSlash(file)))
	}

	// tt runs no hook of a source rock it builds.
	answer, err := os.ReadFile(filepath.Join(rocksTree, "share", "tarantool", "nativedemo",
		"answer.txt"))
	require.NoError(t, err)
	assert.Equal(t, "#define ANSWER 0\n", string(answer))
}
