package emmylua_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/modules/emmylua"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/sdktest"
)

// programs are the programs of a build of EmmyLua.
var programs = []string{"emmylua_check", "emmylua_doc_cli", "emmylua_ls", "luafmt"}

// testBuild writes a fake build of EmmyLua, a directory of executables, and
// returns its path.
func testBuild(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	for _, program := range programs {
		require.NoError(t, os.WriteFile(filepath.Join(dir, program),
			[]byte("#!/bin/sh\necho "+program+"\n"), 0o755))
	}

	return dir
}

// testProject returns a project directory with a manifest.
func testProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.manifest.toml"),
		[]byte("manifest_version = \"0.1\"\n"), 0o644))

	return dir
}

// TestPrepareFrom checks that tt emmylua prepare --from packs the build into
// the registry of the project, declares the rock from it in
// [dev_dependencies] through the project, prints how the lock moved, and
// installs the rocks, the --registry servers given to both.
func TestPrepareFrom(t *testing.T) {
	t.Parallel()

	project := testProject(t)
	services := sdktest.New(t, sdktest.WithProject(project),
		sdktest.WithMoves(
			sdk.DependencyMove{Name: "emmylua", From: "", To: "0.30.0pre1-1"},
			sdk.DependencyMove{Name: "luatest", From: "1.0.0-1", To: "1.1.0-1"},
		))

	got := services.Run(emmylua.New, "emmylua", "prepare", "--from", testBuild(t),
		"--registry", "https://rocks.example/")
	require.NoError(t, got.Err)

	rocks, err := filepath.Glob(filepath.Join(project, ".tarantool", "registry",
		"emmylua-0.30.0pre1-1.*.rock"))
	require.NoError(t, err)
	assert.Len(t, rocks, 1)
	assert.FileExists(t, filepath.Join(project, ".tarantool", "registry", "manifest"))

	assert.Equal(t, []sdk.Dependency{{
		Name: "emmylua", Constraint: "0.30.0pre1", Registry: ".tarantool/registry", Dev: true,
		Registries: []string{"https://rocks.example/"},
	}}, services.AddedDependencies())
	assert.Equal(t, []sdk.InstallOptions{{Registries: []string{"https://rocks.example/"}}},
		services.Installs())

	assert.Equal(t, "  emmylua 0.30.0pre1-1\n"+
		"  luatest 1.0.0-1 -> 1.1.0-1\n"+
		"EmmyLua 0.30.0pre1 from .tarantool/registry\n"+
		"next: tt emmylua check\n", got.Stdout)
	assert.Contains(t, got.Stderr, "reading the build in ")
	assert.Contains(t, got.Stderr,
		"declared emmylua 0.30.0pre1 from .tarantool/registry in [dev_dependencies]")
	assert.Contains(t, got.Stderr, "installing the rocks")
}

// TestPrepareDeclared checks that a declaration already in the manifest is
// left as it is and said so.
func TestPrepareDeclared(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t, sdktest.WithProject(testProject(t)),
		sdktest.WithDeclared(sdk.Dependency{
			Name: "emmylua", Constraint: "0.25.1", Registry: ".tarantool/registry", Dev: true,
			Registries: nil,
		}))

	got := services.Run(emmylua.New, "emmylua", "prepare", "--from", testBuild(t))
	require.NoError(t, got.Err)
	assert.Contains(t, got.Stderr, "emmylua is already declared in app.manifest.toml")
	assert.Len(t, services.Installs(), 1)
}

// TestPrepareRockServer checks that tt emmylua prepare --rock-server packs
// nothing and declares the rock from the server, and that --no-install
// leaves the rocks to tt package build.
func TestPrepareRockServer(t *testing.T) {
	t.Parallel()

	project := testProject(t)
	services := sdktest.New(t, sdktest.WithProject(project))

	got := services.Run(emmylua.New, "emmylua", "prepare", "--version", "0.25.1",
		"--rock-server", "https://rocks.example/", "--no-install")
	require.NoError(t, got.Err)

	assert.NoDirExists(t, filepath.Join(project, ".tarantool"))
	assert.Equal(t, []sdk.Dependency{{
		Name: "emmylua", Constraint: "0.25.1", Registry: "https://rocks.example/", Dev: true,
		Registries: nil,
	}}, services.AddedDependencies())
	assert.Empty(t, services.Installs())
	assert.Equal(t, "EmmyLua 0.25.1 from https://rocks.example/\nnext: tt package build\n",
		got.Stdout)
}

// TestPrepareDefaultRockServer checks that tt emmylua prepare with neither
// --from nor --rock-server packs nothing and declares the default release
// from the index of tarantool-contrib/rocks-repo, and that a version tt does
// not know is declared as it is.
func TestPrepareDefaultRockServer(t *testing.T) {
	t.Parallel()

	const index = "https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/"

	project := testProject(t)
	services := sdktest.New(t, sdktest.WithProject(project))

	got := services.Run(emmylua.New, "emmylua", "prepare")
	require.NoError(t, got.Err)

	assert.NoDirExists(t, filepath.Join(project, ".tarantool"))
	assert.Equal(t, []sdk.Dependency{
		{Name: "emmylua", Constraint: "0.30.0pre1", Registry: index, Dev: true},
	}, services.AddedDependencies())
	assert.Len(t, services.Installs(), 1)
	assert.Equal(t, "EmmyLua 0.30.0pre1 from "+index+"\nnext: tt emmylua check\n", got.Stdout)

	services = sdktest.New(t, sdktest.WithProject(project))

	got = services.Run(emmylua.New, "emmylua", "prepare", "--version", "0.31.0", "--no-install")
	require.NoError(t, got.Err)

	assert.Equal(t, []sdk.Dependency{
		{Name: "emmylua", Constraint: "0.31.0", Registry: index, Dev: true},
	}, services.AddedDependencies())
	assert.Empty(t, services.Installs())
}

// TestPrepareErrors checks the failures of tt emmylua prepare: flags that
// exclude each other, a directory that is not a project, and a declaration
// the project refuses, after which nothing is installed.
func TestPrepareErrors(t *testing.T) {
	t.Parallel()

	got := sdktest.New(t, sdktest.WithProject(testProject(t))).Run(emmylua.New,
		"emmylua", "prepare", "--from", t.TempDir(), "--rock-server", "https://rocks.example/")
	require.ErrorContains(t, got.Err, "none of the others can be")

	got = sdktest.Run(t, emmylua.New, "emmylua", "prepare", "--from", testBuild(t))
	require.ErrorContains(t, got.Err, "not a tt project")

	// Below the root the manifest the SDK edits is not there: the error
	// names the root to run it in, and nothing is declared.
	project := testProject(t)
	sub := filepath.Join(project, "app")
	require.NoError(t, os.Mkdir(sub, 0o755))

	inSub := sdktest.New(t, sdktest.WithProject(sub))

	got = inSub.Run(emmylua.New, "emmylua", "prepare")
	require.EqualError(t, got.Err, "not the root of the project: "+sub+
		" is inside the project "+project+"; run tt emmylua prepare there")
	assert.Empty(t, inSub.AddedDependencies())

	refused := errors.New("the manifest is read-only")
	services := sdktest.New(t, sdktest.WithProject(testProject(t)),
		sdktest.WithAddDependencyError(refused))

	got = services.Run(emmylua.New, "emmylua", "prepare", "--from", testBuild(t))
	require.ErrorIs(t, got.Err, refused)
	require.ErrorContains(t, got.Err, "declaring the emmylua dependency")
	assert.Len(t, services.AddedDependencies(), 1)
	assert.Empty(t, services.Installs())
}

// TestRockFrom checks that tt emmylua rock --from packs the build as the
// rock of --arch into a rock server and prints the paths, and that --arch
// needs --from.
func TestRockFrom(t *testing.T) {
	t.Parallel()

	out := t.TempDir()

	got := sdktest.Run(t, emmylua.New, "emmylua", "rock", "--from", testBuild(t),
		"--arch", "macosx-riscv64", "--out", out)
	require.NoError(t, got.Err)

	rock := filepath.Join(out, "emmylua-0.30.0pre1-1.macosx-riscv64.rock")
	assert.FileExists(t, rock)
	assert.Equal(t, rock+"\n"+filepath.Join(out, "manifest")+"\n", got.Stdout)

	got = sdktest.Run(t, emmylua.New, "emmylua", "rock", "--arch", "linux-x86_64",
		"--out", out)
	require.ErrorContains(t, got.Err, "--arch names the build of --from")

	got = sdktest.Run(t, emmylua.New, "emmylua", "rock", "--from", testBuild(t))
	require.ErrorContains(t, got.Err, `required flag(s) "out" not set`)
}
