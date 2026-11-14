package core

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/cmd"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
)

// This file tests the services of Project that read and extend the
// manifest and the state of the project - Root, Manifest, AddComponent,
// StateDir and PrepareEnvironment - and Rocks.

// realPath returns path with its symbolic links resolved, as the working
// directory reports it.
func realPath(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)

	return resolved
}

// TestProjectRoot checks that a command run below the project works on the
// project: Root and StateDir are of the project, Dir is the directory of
// the command, and AddDependency declares into the manifest of the project.
//
//nolint:paralleltest // Changes the directory, the environment and the CmdCtx.
func TestProjectRoot(t *testing.T) {
	registry := rockServer(t, map[string][]string{"tool": {"1.0.0-1"}})

	created, project := packageProject(t, projectManifest)
	dir := realPath(t, created)

	root, err := project.Root()
	require.NoError(t, err)
	assert.Equal(t, dir, realPath(t, root))

	sub := filepath.Join(dir, "src", "deep")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	t.Chdir(sub)

	here, err := project.Dir()
	require.NoError(t, err)
	assert.Equal(t, sub, realPath(t, here))

	root, err = project.Root()
	require.NoError(t, err)
	assert.Equal(t, dir, realPath(t, root))

	state, err := project.StateDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, ".tarantool"), state)
	assert.NoDirExists(t, state, "it is not created")

	_, err = project.AddDependency(t.Context(), sdk.Dependency{
		Name: "tool", Constraint: "", Registry: registry, Dev: false, Registries: nil,
	})
	require.NoError(t, err)

	_, man, _ := readProject(t, dir)
	assert.Contains(t, man.Dependencies, "tool", "declared in the project above")
	assert.NoFileExists(t, filepath.Join(sub, "app.manifest.toml"))

	// Outside every project the directory of the command is the root.
	outside := realPath(t, t.TempDir())
	t.Chdir(outside)

	root, err = project.Root()
	require.NoError(t, err)
	assert.Equal(t, outside, realPath(t, root))
}

// manifestOfServices is a manifest that declares one of every kind of
// dependency and component the Manifest of a module reads.
const manifestOfServices = `manifest_version = '0.1'

[package]
name = 'app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'

[dependencies]
checks = '>=3.0.0'
wasm = { version = '0.1.0', registry = '.tarantool/registry' }
local = { source = 'path', path = '../local' }

[dev_dependencies]
luatest = '1.0.1'

[components.lua]
path = '.'

[components.router]
path = 'router'

[components.router.build]
backend = 'shell'
command = 'tarawasm'
cwd = 'router/src'
`

// TestProjectManifest checks what a module reads of the manifest, and the
// errors of a project without one and of one that does not parse.
//
//nolint:paralleltest // Changes the directory, the environment and the CmdCtx.
func TestProjectManifest(t *testing.T) {
	dir, project := packageProject(t, manifestOfServices)

	manifest, err := project.Manifest()
	require.NoError(t, err)
	assert.Equal(t, sdk.Manifest{
		Dependencies: map[string]sdk.ManifestDependency{
			"checks": {Version: ">=3.0.0", Registry: "", Path: ""},
			"wasm":   {Version: "0.1.0", Registry: ".tarantool/registry", Path: ""},
			"local":  {Version: "", Registry: "", Path: "../local"},
		},
		DevDependencies: map[string]sdk.ManifestDependency{
			"luatest": {Version: "1.0.1", Registry: "", Path: ""},
		},
		Components: map[string]sdk.ManifestComponent{
			"lua": {Path: ".", Build: nil},
			"router": {
				Path: "router", Build: &sdk.ManifestBuild{Backend: "shell", Cwd: "router/src"},
			},
		},
	}, manifest)

	path := filepath.Join(dir, "app.manifest.toml")
	require.NoError(t, os.WriteFile(path, []byte("[package\n"), 0o644))

	_, err = project.Manifest()
	require.ErrorContains(t, err, "parsing "+path)
	require.NotErrorIs(t, err, sdk.ErrNotFound)

	require.NoError(t, os.Remove(path))

	_, err = project.Manifest()
	require.ErrorIs(t, err, sdk.ErrNotFound)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, path)
}

// routerOfServices is the component the tests of AddComponent declare, as
// tt wasm new declares one.
func routerOfServices() sdk.Component {
	return sdk.Component{
		Name:    "router",
		Comment: "A WebAssembly component, made by tt wasm new and built by tarawasm.",
		Path:    "router",
		Include: []string{"dist/router.wasm"},
		Build: &sdk.ComponentBuild{Backend: "shell", Fields: []sdk.BuildField{
			{Key: "command", Value: "tarawasm"},
			{Key: "args", Value: []string{"all"}},
			{Key: "incremental", Value: true},
		}},
		Registries: nil,
		OnDeclared: nil,
	}
}

// routerBlock is the text AddComponent appends for routerOfServices.
const routerBlock = `
# A WebAssembly component, made by tt wasm new and built by tarawasm.
[components.router]
path = "router"
include = ["dist/router.wasm"]

[components.router.build]
backend = "shell"
command = "tarawasm"
args = ["all"]
incremental = true
`

// TestProjectAddComponent checks that a module declares a component as tt
// wasm new does: appended to the manifest, every line of it kept, and the
// lock resolved again against it, OnDeclared called in between; and what it
// refuses, writing nothing and calling nothing.
//
//nolint:paralleltest // Changes the directory, the environment and the CmdCtx.
func TestProjectAddComponent(t *testing.T) {
	dir, project := packageProject(t, projectManifest)

	declared := 0
	router := routerOfServices()

	router.OnDeclared = func() {
		declared++

		written, err := os.ReadFile(filepath.Join(dir, "app.manifest.toml"))
		require.NoError(t, err)
		assert.Equal(t, projectManifest+routerBlock, string(written), "the manifest is written")
		assert.NoFileExists(t, filepath.Join(dir, "app.manifest.lock"),
			"the lock is not resolved yet")
	}

	change, err := project.AddComponent(t.Context(), router)
	require.NoError(t, err)
	assert.Equal(t, sdk.ComponentChange{Declared: true, Moves: []sdk.DependencyMove{}}, change)
	assert.Equal(t, 1, declared)

	source, man, lock := readProject(t, dir)
	assert.Equal(t, projectManifest+routerBlock, string(source))
	assert.Equal(t, []string{"dist/router.wasm"}, man.Components["router"].Include)
	assert.Equal(t, man.Hash(), lock.ManifestHash, "the lock is of the manifest with it")

	// A name declared already.
	change, err = project.AddComponent(t.Context(), router)
	require.ErrorIs(t, err, cmd.ErrComponentDeclared)
	assert.False(t, change.Declared)

	// A component the manifest cannot carry.
	broken := router

	broken.Name = "broken"
	broken.Build = &sdk.ComponentBuild{
		Backend: "shell",
		Fields: append(slices.Clone(router.Build.Fields),
			sdk.BuildField{Key: "static", Value: "yes"}),
	}

	change, err = project.AddComponent(t.Context(), broken)
	require.ErrorContains(t, err, "the manifest with component broken")
	assert.False(t, change.Declared)
	assert.Equal(t, 1, declared, "OnDeclared is not called when nothing is written")

	unchanged, _, _ := readProject(t, dir)
	assert.Equal(t, string(source), string(unchanged), "nothing is written")
}

// TestProjectAddComponentErrors checks a resolution that fails, which
// leaves the component declared, and a tt with no Tarantool and a project
// with no manifest, which declare nothing.
//
//nolint:paralleltest // Changes the directory, the environment and the CmdCtx.
func TestProjectAddComponentErrors(t *testing.T) {
	// No server TT_REGISTRIES names has missing.
	content := projectManifest + "\n[dependencies]\nmissing = '>=1.0.0'\n"
	dir, project := packageProject(t, content)

	declared := 0
	router := routerOfServices()

	router.OnDeclared = func() { declared++ }

	change, err := project.AddComponent(t.Context(), router)
	require.ErrorContains(t, err, "re-resolving the lock")
	require.ErrorContains(t, err, "rock not found on any server")
	assert.True(t, change.Declared)
	assert.Equal(t, 1, declared, "the component is declared before the resolution fails")

	source, err := os.ReadFile(filepath.Join(dir, "app.manifest.toml"))
	require.NoError(t, err)
	assert.Equal(t, content+routerBlock, string(source), "the component stays declared")

	// A tt with no Tarantool, whose rockspecs cannot be evaluated.
	cmd.GetCmdCtxPtr().Cli.TarantoolCli = cmdcontext.TarantoolCli{Executable: ""}

	other := router

	other.Name = "other"

	change, err = project.AddComponent(t.Context(), other)
	require.ErrorIs(t, err, sdk.ErrNotFound)
	assert.False(t, change.Declared)
	assert.Equal(t, 1, declared)

	unchanged, err := os.ReadFile(filepath.Join(dir, "app.manifest.toml"))
	require.NoError(t, err)
	assert.Equal(t, string(source), string(unchanged))

	_, empty := packageProject(t, "")

	change, err = empty.AddComponent(t.Context(), routerOfServices())
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.False(t, change.Declared)
}

// TestProjectPrepareEnvironment checks that the scripts of the environment
// go to the root of the project, from a directory below it too, and that
// scripts that are there are kept.
//
//nolint:paralleltest // Changes the directory, the environment and the CmdCtx.
func TestProjectPrepareEnvironment(t *testing.T) {
	created, project := packageProject(t, projectManifest)
	dir := realPath(t, created)

	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	t.Chdir(sub)

	require.NoError(t, project.PrepareEnvironment())

	activate := filepath.Join(dir, ".env", "activate.sh")
	script, err := os.ReadFile(activate)
	require.NoError(t, err)
	assert.Contains(t, string(script), "'"+dir+"'", "it activates the project")
	assert.FileExists(t, filepath.Join(dir, ".env", "deactivate.sh"))
	assert.NoDirExists(t, filepath.Join(sub, ".env"))

	require.NoError(t, os.WriteFile(activate, []byte("# mine\n"), 0o644))
	require.NoError(t, project.PrepareEnvironment())

	script, err = os.ReadFile(activate)
	require.NoError(t, err)
	assert.Equal(t, "# mine\n", string(script), "the user's script is kept")
}

// TestRocksIndex checks that a module makes a directory a rock server with
// the rock index, and with IfIndexed one without an index a server of
// LuaRocks manifests alone.
//
//nolint:paralleltest // Replaces the process logger.
func TestRocksIndex(t *testing.T) {
	restoreProcess(t)

	rocks := readyServices(strings.NewReader(""), io.Discard, io.Discard).Rocks()

	indexed := t.TempDir()
	writeRock(t, filepath.Join(indexed, "tool-1.0.0-1.all.rock"), "tool", "1.0.0-1")

	require.NoError(t, rocks.Index(t.Context(), indexed, sdk.IndexOptions{IfIndexed: false}))
	assert.FileExists(t, filepath.Join(indexed, "manifest"))

	index, err := os.ReadFile(filepath.Join(indexed, "tt-rocks-index.json"))
	require.NoError(t, err)
	assert.Contains(t, string(index), "tool-1.0.0-1.all.rock")

	// An index that is there is kept current.
	writeRock(t, filepath.Join(indexed, "tool-1.1.0-1.all.rock"), "tool", "1.1.0-1")

	require.NoError(t, rocks.Index(t.Context(), indexed, sdk.IndexOptions{IfIndexed: true}))

	index, err = os.ReadFile(filepath.Join(indexed, "tt-rocks-index.json"))
	require.NoError(t, err)
	assert.Contains(t, string(index), "tool-1.1.0-1.all.rock")

	// A directory with none gets none.
	plain := t.TempDir()
	writeRock(t, filepath.Join(plain, "tool-1.0.0-1.all.rock"), "tool", "1.0.0-1")

	require.NoError(t, rocks.Index(t.Context(), plain, sdk.IndexOptions{IfIndexed: true}))
	assert.FileExists(t, filepath.Join(plain, "manifest"))
	assert.NoFileExists(t, filepath.Join(plain, "tt-rocks-index.json"))

	err = rocks.Index(t.Context(), filepath.Join(plain, "missing"), sdk.IndexOptions{})
	require.ErrorContains(t, err, "missing")
}
