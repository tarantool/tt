package pack

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/go-luarocks/manif"
	"github.com/tarantool/go-luarocks/remote"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

// pureManifest is a package of Lua and data alone: a module with a data file
// under its own name, a role under roles and a module of no namespace. Its
// repository is %s.
const pureManifest = `manifest_version = '0.1'

[package]
name = 'puredemo'
license = 'BSD-2-Clause'
license_files = ['LICENSE']
repository = '%s'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[components.lib]
path = 'puredemo'
include = ['*.lua', '*.json']

[components.roles]
path = 'roles'
namespace = 'roles'

[components.flat]
path = 'flat'
namespace = ''
`

// pureGitignore is what git ignores in the project of pureManifest.
const pureGitignore = "/_build/\n/.rocks/\n/_out/\n/app.manifest.lock\n"

// writePurePackage writes the project of pureManifest as a git repository
// whose HEAD is the tag v1.2.0, the repository of the package at its
// git+file URL, with files, by path, written over its own before the commit.
func writePurePackage(t *testing.T, files map[string]string) string {
	t.Helper()

	src := t.TempDir()
	project := map[string]string{
		manifestFileName:            fmt.Sprintf(pureManifest, "git+file://"+src),
		".gitignore":                pureGitignore,
		"LICENSE":                   "BSD-2-Clause text\n",
		"README.md":                 "# puredemo\n",
		"puredemo/init.lua":         "return { str = require('puredemo.util.str') }\n",
		"puredemo/util/str.lua":     "return {}\n",
		"puredemo/data/schema.json": "{}\n",
		"roles/puredemo.lua":        "return {}\n",
		"flat/top.lua":              "return {}\n",
	}
	maps.Copy(project, files)

	writeTree(t, src, project)
	git(t, src, "init", "-q")
	git(t, src, "add", "-A")
	git(t, src, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	git(t, src, "tag", "v1.2.0")

	return src
}

// buildTable reads the build table of the rockspec at path.
func buildTable(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	source := string(data)

	start := strings.Index(source, "\nbuild = {\n")
	end := strings.Index(source, "\ndeploy = {\n")
	require.True(t, start >= 0 && end > start, source)

	return source[start+1 : end+1]
}

// TestPackRockspecOfAPurePackage: the rockspec of a package with no build
// block installs, by build.install, every file its layout takes where the
// binary rock has it, and _version.lua from build.extra_files, the bytes of
// the binary rock, so that LuaRocks builds the source rock with no tool at
// all. A pack of the source rock alone, which builds nothing, writes the same
// rockspec; a package that generates no _version.lua carries none.
func TestPackRockspecOfAPurePackage(t *testing.T) {
	t.Parallel()

	src := writePurePackage(t, nil)

	var warnings []string

	opts := packOptions(src, filepath.Join(src, "_out", "both"), FormatRock, FormatRockSrc)

	opts.Warn = func(msg string) { warnings = append(warnings, msg) }

	result, err := Run(context.Background(), opts)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	versionLua := zipEntries(t, result.Rock)["lua/puredemo/_version.lua"]
	require.Contains(t, versionLua, `version  = "1.2.0",`)

	assert.Equal(t, `build = {
   type = "none",
   install = {
      lua = {
         ["puredemo._version"] = "tt-rock-version.lua",
         ["puredemo.data.schema"] = "puredemo/data/schema.json",
         ["puredemo.init"] = "puredemo/init.lua",
         ["puredemo.util.str"] = "puredemo/util/str.lua",
         ["roles.puredemo"] = "roles/puredemo.lua",
         ["top"] = "flat/top.lua",
      },
   },
   extra_files = {
      ["tt-rock-version.lua"] = [=[
`+versionLua+`]=],
   },
}
`, buildTable(t, result.Rockspec))

	rockspec, err := os.ReadFile(result.Rockspec)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(rockspec),
		"-- The rock of the tt package puredemo 1.2.0.\n"), string(rockspec))

	srcOnly, err := Run(context.Background(),
		packOptions(src, filepath.Join(src, "_out", "src"), FormatRockSrc))
	require.NoError(t, err)

	alone, err := os.ReadFile(srcOnly.Rockspec)
	require.NoError(t, err)
	assert.Equal(t, string(rockspec), string(alone))

	writeTree(t, src, map[string]string{
		manifestFileName: strings.Replace(fmt.Sprintf(pureManifest, "git+file://"+src),
			"license = ", "generate_version_lua = false\nlicense = ", 1),
	})

	result, err = Run(context.Background(),
		packOptions(src, filepath.Join(src, "_out", "bare"), FormatRock))
	require.NoError(t, err)

	table := buildTable(t, result.Rockspec)
	assert.NotContains(t, table, "extra_files")
	assert.NotContains(t, table, "version")
	assert.Contains(t, table, `["puredemo.init"] = "puredemo/init.lua",`)
}

// TestSrcRockBuildsWithLuaRocks: LuaRocks with no tt and no manifest -
// go-luarocks natively, and running the LuaRocks 3.9.2 that tt rocks of tt 2
// is - installs the source rock of a package with no build block, and its
// bare rockspec from the git repository of the package, which holds no file
// the build generates, with the files of its binary rock: lua/, lib/ and
// bin/ alike to the byte, and doc/ with more, as LuaRocks adds the README of
// the root to the license files.
func TestSrcRockBuildsWithLuaRocks(t *testing.T) {
	t.Parallel()

	src := writePurePackage(t, nil)

	result, err := Run(context.Background(),
		packOptions(src, filepath.Join(t.TempDir(), "out"), FormatRock, FormatRockSrc))
	require.NoError(t, err)

	for name, backend := range map[string]client.Backend{
		"native": client.BackendNative, "lua": client.BackendLua,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			binary := luaRocksInstall(t, backend, result.Rock)
			require.NoError(t, binary.err)

			want := installedRock(t, binary.tree, result.Rockspec)
			require.Contains(t, want.Lua, "puredemo/_version.lua")
			require.Contains(t, want.Lua, "top.lua")
			require.Contains(t, want.Doc, "LICENSE")

			srcRock := luaRocksInstall(t, backend, result.SrcRock)
			require.NoError(t, srcRock.err)

			bare := luaRocksBuild(t, backend, result.Rockspec)
			require.NoError(t, bare.err)

			for what, tree := range map[string]string{
				"source rock": srcRock.tree, "bare rockspec": bare.tree,
			} {
				got := installedRock(t, tree, result.Rockspec)

				assert.Equal(t, want.Lua, got.Lua, what)
				assert.Equal(t, want.Lib, got.Lib, what)
				assert.Equal(t, want.Bin, got.Bin, what)

				for doc, sum := range want.Doc {
					assert.Equal(t, sum, got.Doc[doc], "%s: doc/%s", what, doc)
				}

				assert.Contains(t, got.Doc, "README.md", what)
			}
		})
	}
}

// TestPackRockspecLeavesOutWhatTheSourceRockLacks: a file the layout takes
// that the source rock does not carry - one git does not track, or one it
// ignores - is in the binary rock but not in build.install, where LuaRocks
// would fail on it, and the pack of the source rock warns of each once.
// LuaRocks builds the source rock without them, as tt does.
func TestPackRockspecLeavesOutWhatTheSourceRockLacks(t *testing.T) {
	t.Parallel()

	src := writePurePackage(t, map[string]string{".gitignore": pureGitignore + "gen.lua\n"})
	writeTree(t, src, map[string]string{
		"puredemo/new.lua": "return 1\n",
		"puredemo/gen.lua": "return 2\n",
	})

	var warnings []string

	opts := packOptions(src, filepath.Join(t.TempDir(), "out"), FormatRock, FormatRockSrc)

	opts.Warn = func(msg string) { warnings = append(warnings, msg) }

	result, err := Run(context.Background(), opts)
	require.NoError(t, err)

	entries := zipEntries(t, result.Rock)
	assert.Contains(t, entries, "lua/puredemo/new.lua")
	assert.Contains(t, entries, "lua/puredemo/gen.lua")

	table := buildTable(t, result.Rockspec)
	assert.NotContains(t, table, "puredemo/new.lua")
	assert.NotContains(t, table, "puredemo/gen.lua")
	assert.Contains(t, table, `["puredemo.init"] = "puredemo/init.lua",`)

	require.Len(t, warnings, 2, warnings)

	var untracked, ignored string

	for _, warning := range warnings {
		switch {
		case strings.Contains(warning, "git does not track"):
			untracked = warning
		case strings.Contains(warning, "git ignores"):
			ignored = warning
		}
	}

	assert.Contains(t, untracked, "puredemo/new.lua")
	assert.NotContains(t, untracked, "puredemo/gen.lua")
	assert.Contains(t, ignored, "puredemo/gen.lua")
	assert.NotContains(t, ignored, "puredemo/new.lua")

	built := luaRocksInstall(t, client.BackendNative, result.SrcRock)
	require.NoError(t, built.err)

	lua := installedRock(t, built.tree, result.Rockspec).Lua
	assert.Contains(t, lua, "puredemo/init.lua")
	assert.NotContains(t, lua, "puredemo/new.lua")
	assert.NotContains(t, lua, "puredemo/gen.lua")
}

// TestSrcRockOfABuildFailsWithLuaRocks: the source rock of a package with a
// build that does not export builds with tt alone - one of a backend that
// does not say its builds are made without tt, one whose Export fails with
// ErrNoExport, whatever the others do: the pack warns so, naming the field of
// the second, and LuaRocks fails on it, printing which builds need tt, where
// it used to install it with no module. tt builds it from its manifest as
// ever.
func TestSrcRockOfABuildFailsWithLuaRocks(t *testing.T) {
	t.Parallel()

	src := writeGitPackage(t, map[string]string{
		manifestFileName: libraryManifest + `
[components.gen]
path = 'gen'

[components.gen.build]
backend = 'exe'

[components.tree]
path = 'tree'

[components.tree.build]
backend = 'tree'
variables = { X = '1' }
output = [{ path = 'data.json', to = 'share' }]

[hooks.post_build]
backend = 'shell'
command = 'true'
`,
		"gen/README":     "the generated program\n",
		"tree/data.json": "{}\n",
	})

	out := filepath.Join(t.TempDir(), "server")

	var warnings []string

	opts := packOptions(src, out, FormatRockSrc)

	opts.Warn = func(msg string) { warnings = append(warnings, msg) }

	result, err := Run(context.Background(), opts)
	require.NoError(t, err)

	const reasons = `the component "gen" is built by the tt backend "exe", which builds ` +
		`with tt alone; the component "tree" is built by the tt backend "tree" with tt ` +
		`alone, for components.tree.build.variables: the variables of a tree are set by ` +
		`tt alone`

	const message = `mylib 1.2.0 builds with tt only: ` + reasons + `. Install its binary ` +
		`rock, or depend on it in app.manifest.toml of a project tt 3 builds.`

	table := buildTable(t, result.Rockspec)
	assert.True(t, strings.HasPrefix(table, "build = {\n   type = \"command\",\n"+
		"   build_command = \"printf '%s\\\\n' 'mylib 1.2.0 builds with tt only: "), table)
	assert.NotContains(t, table, "install")
	assert.NotContains(t, table, "extra_files")

	rockspec, err := os.ReadFile(result.Rockspec)
	require.NoError(t, err)
	assert.Contains(t, string(rockspec), "LuaRocks cannot, as the build table says.")

	assert.Equal(t, []string{`the source rock builds with tt only, which builds it from ` +
		`its manifest: ` + reasons + `; LuaRocks, tt rocks included, fails to build it, ` +
		`saying so`}, warnings)

	native := luaRocksInstall(t, client.BackendNative, result.SrcRock)
	require.ErrorContains(t, native.err, "(stderr: "+message+")")
	assert.NoDirExists(t, filepath.Join(native.tree, "share", "tarantool", "mylib"))

	// The LuaRocks of tt 2 prints the message to the stderr of the command,
	// that of the test here, and fails the build.
	lua := luaRocksInstall(t, client.BackendLua, result.SrcRock)
	require.ErrorContains(t, lua.err, "Failed building")
	assert.NoDirExists(t, filepath.Join(lua.tree, "share", "tarantool", "mylib"))

	consumer := buildConsumer(t, out)
	share := filepath.Join(consumer, rocksDirName, "share", "tarantool", "mylib")
	assert.FileExists(t, filepath.Join(share, "init.lua"))
	assert.FileExists(t, filepath.Join(share, "data.json"))
	assert.FileExists(t, filepath.Join(consumer, rocksDirName, "bin", "gen"))
}

// TestPackRockRefusesReservedNames: a file of the sources of the rock at a
// name the rock reserves at their root fails a pack of the rock, whose
// rockspec is that of the source rock; a file git does not track is in no
// source of the rock and fails nothing.
func TestPackRockRefusesReservedNames(t *testing.T) {
	t.Parallel()

	for file, top := range map[string]string{
		"tt-rock.sh":                "tt-rock.sh",
		"tt-rock-version.lua":       "tt-rock-version.lua",
		".tt-rock/stage/x":          ".tt-rock",
		".luarocks.patches.applied": ".luarocks.patches.applied",
	} {
		src := writePurePackage(t, map[string]string{file: "x\n"})

		_, err := Run(context.Background(),
			packOptions(src, filepath.Join(t.TempDir(), "out"), FormatRock))
		require.ErrorIs(t, err, errRockReserved, file)
		require.ErrorContains(t, err, "the project has "+top+" at its root", file)
		assert.Equal(t, sdk.ExitFailure, exitcode.Code(err), file)
	}

	src := writePurePackage(t, nil)
	writeTree(t, src, map[string]string{"tt-rock.sh": "x\n"})

	_, err := Run(context.Background(),
		packOptions(src, filepath.Join(t.TempDir(), "out"), FormatRock, FormatRockSrc))
	require.NoError(t, err)
}

// TestPackRockRefusesInexpressiblePaths: a path of the layout that
// build.install cannot say fails the pack of a package with no build block,
// with a hint naming the component; the same path in a package with a build
// block fails nothing, as its script lays the file out.
func TestPackRockRefusesInexpressiblePaths(t *testing.T) {
	t.Parallel()

	for file, reason := range map[string]string{
		"puredemo/v1.2/schema.json": `the name of the directory "v1.2" has a dot`,
		"puredemo/init.min.lua":     `the name of the Lua file "init.min.lua" has a dot`,
	} {
		src := writePurePackage(t, map[string]string{file: "{}\n"})

		_, err := Run(context.Background(),
			packOptions(src, filepath.Join(t.TempDir(), "out"), FormatRockSrc))
		require.ErrorIs(t, err, rocktree.ErrInstallDest, file)
		require.ErrorContains(t, err, reason, file)
		require.ErrorContains(t, err, `the layout of the component "lib" takes `+file+
			" there: rename it, or leave it out of the layout with the exclude of "+
			"[components.lib]", file)
		assert.Equal(t, sdk.ExitFailure, exitcode.Code(err), file)
	}

	src := writePurePackage(t, map[string]string{
		"puredemo/init.min.lua": "return 1\n",
		manifestFileName: fmt.Sprintf(pureManifest, "git+file://") + `
[hooks.pre_build]
backend = 'shell'
command = 'true'
`,
	})

	result, err := Run(context.Background(),
		packOptions(src, filepath.Join(t.TempDir(), "out"), FormatRockSrc))
	require.NoError(t, err)

	table := buildTable(t, result.Rockspec)
	assert.Contains(t, table, "\ttt_lay puredemo/init.min.lua lua/puredemo/init.min.lua\n")
	assert.NotContains(t, table, `= "puredemo/init.min.lua"`)
}

// luaRocksRun is what a run of go-luarocks did: the tree it worked on, and
// how it failed, if it did.
type luaRocksRun struct {
	tree string
	err  error
}

// luaRocksClient is a go-luarocks client of backend, as LuaRocks is with no
// tt: no manifest, and the tree at tree of a Tarantool 3.0.0, from the rock
// server server. The Tarantool is one of a prefix with the headers
// LuaRocks checks LUA_INCDIR for, and a tarantool that runs nothing, which
// LuaRocks asks of its LuaJIT; its version is as tarantool --version says
// it, which the LuaRocks of tt 2 provides the rock tarantool of.
func luaRocksClient(t *testing.T, backend client.Backend, tree, server string) *client.Rocks {
	t.Helper()

	prefix := t.TempDir()
	writeTree(t, prefix, map[string]string{
		"include/tarantool/lua.h":    "#define LUA_VERSION_NUM 501\n",
		"include/tarantool/module.h": "",
		"bin/tarantool":              "#!/bin/sh\nexit 0\n",
	})
	require.NoError(t, os.Chmod(filepath.Join(prefix, "bin", "tarantool"), 0o755))

	cfg := rocks.BuildConfig(rocks.TarantoolInfo{
		Executable: filepath.Join(prefix, "bin", "tarantool"), Prefix: prefix,
		Version: "3.0.0-0-g0123456",
	}, rocks.ConfigOptions{
		Tree: tree, WorkingDir: t.TempDir(), Servers: []string{server}, Logger: nil,
	})

	rocksClient, err := client.New(cfg, client.WithBackend(backend))
	require.NoError(t, err)

	return rocksClient
}

// rockOf is the name and the version of the rock, or of the rockspec, at
// path, by its file name.
func rockOf(path string) (string, string) {
	base := strings.TrimSuffix(filepath.Base(path), rockspecExt)
	if trimmed, ok := strings.CutSuffix(base, rockExt); ok {
		base = trimmed[:strings.LastIndex(trimmed, ".")]
	}

	name, version, _ := strings.Cut(base, "-")

	return name, version
}

// luaRocksInstall installs the rock at path from a rock server holding it
// alone into a new tree with go-luarocks of backend, without its
// dependencies, which LuaRocks has the Tarantool of for one.
func luaRocksInstall(t *testing.T, backend client.Backend, rock string) luaRocksRun {
	t.Helper()

	server := t.TempDir()

	data, err := os.ReadFile(rock)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(server, filepath.Base(rock)), data, filePerm))
	require.NoError(t, remote.MakeManifest(context.Background(), server,
		remote.MakeManifestOpts{}))

	name, _ := rockOf(rock)
	tree := t.TempDir()

	err = luaRocksClient(t, backend, tree, server).Install(context.Background(), name,
		client.InstallOpts{Deps: client.DepsNone})

	return luaRocksRun{tree: tree, err: err}
}

// luaRocksBuild builds the bare rockspec at path, from the source it names,
// into a new tree with go-luarocks of backend.
func luaRocksBuild(t *testing.T, backend client.Backend, rockspec string) luaRocksRun {
	t.Helper()

	tree := t.TempDir()
	err := luaRocksClient(t, backend, tree, t.TempDir()).Build(context.Background(), rockspec,
		client.BuildOpts{})

	return luaRocksRun{tree: tree, err: err}
}

// installedRock reads the rock_manifest in tree of the rock of rock, a rock
// or a rockspec (rockOf): the files of each section of the rock, by path, with
// their md5.
func installedRock(t *testing.T, tree, rock string) *luarocks.RockManifest {
	t.Helper()

	name, version := rockOf(rock)

	rm, err := manif.FileStore{}.ReadRock(filepath.Join(tree, "share", "tarantool", "rocks",
		name, version, "rock_manifest"))
	require.NoError(t, err)

	return rm
}
