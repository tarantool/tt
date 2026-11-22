package core

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkbuild "github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/sdk/v3"
	"github.com/tarantool/tt/v3/cli/cmd"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
)

// This file tests the build services of Project - Build and BuildContext -
// against tt package build.

// wasmBuild is the build of the wasm backend of the tests: it writes each
// file its output lists into the working directory of the build, with the
// arguments of the build in it, and leaves the copy to the core.
type wasmBuild struct {
	output []sdkbuild.OutputFile
}

// Run writes the files of output.
func (b wasmBuild) Run(_ context.Context, facts *sdkbuild.Context) error {
	for _, file := range b.output {
		path := filepath.Join(facts.Dirs.Cwd, file.Path)

		err := os.MkdirAll(filepath.Dir(path), 0o755)
		if err != nil {
			return err
		}

		content := "\x00asm\x01\x00\x00\x00" + strings.Join(facts.Args, "")

		err = os.WriteFile(path, []byte(content), 0o644)
		if err != nil {
			return err
		}
	}

	return nil
}

// wasmBackend is a backend of data files only, as the wasm backend of the
// spec of tt wasm is: what its output lists the core copies into share/.
func wasmBackend() sdkbuild.Backend {
	return sdkbuild.Backend{
		Name:     "wasm",
		Produces: []sdkbuild.Result{sdkbuild.Data},
		Config: func() any {
			return new(struct {
				Language string `toml:"language"`
			})
		},
		New: func(_ any, _ sdkbuild.Component, common sdkbuild.Common) (sdkbuild.Build, error) {
			return wasmBuild{output: common.Output}, nil
		},
		Incremental:    true,
		OutputFrom:     sdkbuild.OutputCwd,
		Exclude:        []string{"/dist/"},
		NoSourceLayout: true,
		Hook:           false,
	}
}

// wasmProject is a package with a component of the wasm backend.
const wasmProject = projectManifest + `
[components.calc]
path = 'components/calc'

[components.calc.build]
backend = 'wasm'
language = 'python'
output = 'dist/calc.wasm'
incremental = true
`

// withWasmBackend makes the registry of the process one with the wasm
// backend, for the test.
func withWasmBackend(t *testing.T) {
	t.Helper()

	previous := buildreg.Process()
	registry := buildregtest.Registry()

	require.NoError(t, registry.Add("wasm", []sdkbuild.Backend{wasmBackend()}, nil))
	buildreg.SetProcess(registry)

	t.Cleanup(func() { buildreg.SetProcess(previous) })
}

// treeOf returns the files of the tree of the project in dir, and of the
// record of what the components built, by their paths relative to dir,
// with their contents; _version.lua, which has the time of the build, is
// named and not read.
func treeOf(t *testing.T, dir string) map[string]string {
	t.Helper()

	files := map[string]string{}

	for _, root := range []string{".rocks", filepath.Join("_build", "results.json")} {
		err := filepath.WalkDir(filepath.Join(dir, root),
			func(path string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}

				rel, err := filepath.Rel(dir, path)
				if err != nil {
					return err
				}

				if entry.Name() == "_version.lua" || entry.Name() == "rock_manifest" {
					files[rel] = ""

					return nil
				}

				//nolint:gosec // The test walks a directory of its own.
				data, err := os.ReadFile(path)

				files[rel] = string(data)

				return err
			})
		require.NoError(t, err)
	}

	return files
}

// TestProjectBuild checks that Build of a component builds what
// tt package build COMPONENT builds, the same tree and record, reports what
// it made where the rock of the package installed it and whether it was up
// to date, and hands the arguments to the build, which they make no longer
// up to date.
//
//nolint:paralleltest // Changes the directory, the environment and the CmdCtx.
func TestProjectBuild(t *testing.T) {
	dir, project := packageProject(t, wasmProject)
	withWasmBackend(t)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "components", "calc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "components", "calc", "main.py"),
		[]byte("def add(a, b): return a + b\n"), 0o644))

	wasm := filepath.Join(dir, ".rocks", "share", "tarantool", "app", "calc.wasm")

	result, err := project.Build(t.Context(), sdk.BuildOptions{Components: []string{"calc"}})
	require.NoError(t, err)
	assert.Equal(t, sdk.BuildResult{
		Product: "default", Version: result.Version,
		Components: map[string]sdk.BuiltComponent{"calc": {
			Modules: []string{}, Data: []string{wasm}, Programs: []string{}, UpToDate: false,
		}},
	}, result)
	assert.FileExists(t, wasm)

	built := treeOf(t, dir)
	assert.Contains(t, built, filepath.Join(".rocks", "share", "tarantool", "app", "calc.wasm"))
	assert.JSONEq(t, `{"components": {"calc": {"data": ["calc.wasm"]}}}`,
		built[filepath.Join("_build", "results.json")])

	// tt package build calc, from scratch, builds the same.
	for _, path := range []string{".rocks", "_build", "app.manifest.lock"} {
		require.NoError(t, os.RemoveAll(filepath.Join(dir, path)))
	}

	pkg := cmd.NewPackageCmd()
	pkg.SetArgs([]string{"build", "calc"})
	require.NoError(t, pkg.Execute())
	assert.Equal(t, built, treeOf(t, dir))

	// Again: up to date.
	result, err = project.Build(t.Context(), sdk.BuildOptions{Components: []string{"calc"}})
	require.NoError(t, err)
	assert.True(t, result.Components["calc"].UpToDate)

	// The jobs are no input: other ones keep it up to date.
	result, err = project.Build(t.Context(), sdk.BuildOptions{
		Components: []string{"calc"}, Jobs: 3,
	})
	require.NoError(t, err)
	assert.True(t, result.Components["calc"].UpToDate)

	// With arguments the build is not up to date, and they reach it.
	result, err = project.Build(t.Context(), sdk.BuildOptions{
		Components: []string{"calc"}, Args: []string{"-opt=2"},
	})
	require.NoError(t, err)
	assert.False(t, result.Components["calc"].UpToDate)

	data, err := os.ReadFile(wasm)
	require.NoError(t, err)
	assert.Equal(t, "\x00asm\x01\x00\x00\x00-opt=2", string(data))

	// Arguments go to one component.
	_, err = project.Build(t.Context(), sdk.BuildOptions{Args: []string{"-opt=2"}})
	require.ErrorContains(t, err, "the arguments of a build go to the build of one component")
	assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))

	// A component the product does not build.
	_, err = project.Build(t.Context(), sdk.BuildOptions{Components: []string{"nope"}})
	require.ErrorContains(t, err, `"nope" is not a component of the selected product`)
}

// TestProjectBuildContext checks that BuildContext gives a module the
// Context a build of its component runs in, and the build of its block,
// with nothing built.
//
//nolint:paralleltest // Changes the directory, the environment and the CmdCtx.
func TestProjectBuildContext(t *testing.T) {
	dir, project := packageProject(t, wasmProject)
	withWasmBackend(t)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "components", "calc"), 0o755))

	facts, bld, err := project.BuildContext(t.Context(), sdk.BuildContextOptions{
		Product: "", Component: "calc", Locked: true, Args: []string{"--help"}, Jobs: 2,
	})
	require.NoError(t, err)
	assert.IsType(t, wasmBuild{}, bld)
	assert.Equal(t, 2, facts.Jobs)
	assert.Equal(t, "2", facts.Env["TT_BUILD_JOBS"])
	assert.Equal(t, sdkbuild.Component{
		Name: "calc", Path: "components/calc", Namespace: "app", Package: "app", Wants: nil,
	}, facts.Component)
	assert.Equal(t, []string{"--help"}, facts.Args)
	assert.True(t, facts.Locked)
	assert.Equal(t, filepath.Join(dir, "components", "calc"), facts.Dirs.Cwd)
	assert.DirExists(t, facts.Dirs.Work, "TT_BUILD_DIR is made")
	assert.Equal(t, "calc", facts.Env["TT_COMPONENT_NAME"])
	assert.NotEmpty(t, facts.Env["TT_VERSION"])

	out, err := facts.Output(facts.Command(t.Context(), "sh", "-c", `printf %s "$TT_BUILD_DIR"`))
	require.NoError(t, err)
	assert.Equal(t, facts.Dirs.Work, string(out), "its commands run in the environment of a build")

	_, ok := facts.LockedVersion("wasm")
	assert.False(t, ok, "no lock")
	assert.NoFileExists(t, filepath.Join(dir, "app.manifest.lock"), "nothing is resolved")
	assert.NoDirExists(t, filepath.Join(dir, ".rocks"), "nothing is built")

	_, _, err = project.BuildContext(t.Context(), sdk.BuildContextOptions{Component: "lua"})
	require.ErrorContains(t, err, "the component has no build block: lua")

	_, _, err = project.BuildContext(t.Context(), sdk.BuildContextOptions{Component: "nope"})
	require.ErrorContains(t, err, `"nope" is not a component of the selected product`)
}

// TestProjectClean checks that after Clean of a component its next build
// runs, as tt wasm clean NAME needs: a module that removed the files it
// recorded, dist/calc.wasm here, would otherwise have the incremental build
// skip the component, its inputs unchanged, and the file stay missing.
//
//nolint:paralleltest // Changes the directory, the environment and the CmdCtx.
func TestProjectClean(t *testing.T) {
	dir, project := packageProject(t, wasmProject)
	withWasmBackend(t)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "components", "calc"), 0o755))

	dist := filepath.Join(dir, "components", "calc", "dist", "calc.wasm")
	wasm := filepath.Join(dir, ".rocks", "share", "tarantool", "app", "calc.wasm")
	work := filepath.Join(dir, "_build", "component", "calc")
	calc := sdk.CleanOptions{Components: []string{"calc"}, DryRun: false}

	_, err := project.Build(t.Context(), sdk.BuildOptions{Components: []string{"calc"}})
	require.NoError(t, err)
	require.FileExists(t, dist)

	dry, err := project.Clean(t.Context(), sdk.CleanOptions{Components: calc.Components,
		DryRun: true})
	require.NoError(t, err)
	assert.Contains(t, dry.Removed, work)
	assert.Contains(t, dry.Removed, wasm)
	assert.FileExists(t, wasm, "a dry run removes nothing")

	// The module removes what it recorded, then the core's state of calc.
	require.NoError(t, os.Remove(dist))

	cleaned, err := project.Clean(t.Context(), calc)
	require.NoError(t, err)
	assert.Equal(t, dry.Removed, cleaned.Removed)
	assert.NoFileExists(t, wasm)
	assert.NoDirExists(t, work)

	result, err := project.Build(t.Context(), sdk.BuildOptions{Components: []string{"calc"}})
	require.NoError(t, err)
	assert.False(t, result.Components["calc"].UpToDate, "a component cleaned is built")
	assert.FileExists(t, dist)
	assert.FileExists(t, wasm)

	_, err = project.Clean(t.Context(), sdk.CleanOptions{Components: []string{"nope"},
		DryRun: false})
	require.ErrorIs(t, err, sdk.ErrNotFound)
	assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))

	// tt package clean cleans the whole project: _build/ but the lock, and
	// the rock of the package.
	pkg := cmd.NewPackageCmd()
	pkg.SetArgs([]string{"clean"})
	require.NoError(t, pkg.Execute())

	entries, err := os.ReadDir(filepath.Join(dir, "_build"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "tt.lock", entries[0].Name())
	assert.NoFileExists(t, wasm)
	assert.FileExists(t, filepath.Join(dir, "app.manifest.lock"))
}

// TestProjectLock checks that the methods of Project work under the lock
// Lock takes when given its context, and that another goroutine waits for
// its release.
//
//nolint:paralleltest // Changes the directory, the environment and the CmdCtx.
func TestProjectLock(t *testing.T) {
	dir, project := packageProject(t, wasmProject)
	withWasmBackend(t)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "components", "calc"), 0o755))

	ctx, release, err := project.Lock(t.Context())
	require.NoError(t, err)

	// Under the lock: an edit, a build and a clean, which would wait for the
	// lock forever with another context.
	require.NoError(t, project.UpdateComponent(ctx, sdk.ComponentUpdate{
		Name: "calc", Fields: nil, Build: []sdk.BuildField{{Key: "language", Value: "rust"}},
		Remove: nil,
	}))

	_, err = project.Build(ctx, sdk.BuildOptions{Components: []string{"calc"}})
	require.NoError(t, err)

	_, err = project.Clean(ctx, sdk.CleanOptions{Components: []string{"calc"}, DryRun: false})
	require.NoError(t, err)

	nested, nestedRelease, err := project.Lock(ctx)
	require.NoError(t, err)
	nestedRelease()

	_, err = project.Build(nested, sdk.BuildOptions{Components: []string{"calc"}})
	require.NoError(t, err, "the release of a nested lock releases nothing")

	// Another goroutine, with its own context, waits for the release.
	done := make(chan error, 1)

	go func() {
		_, err := project.Clean(context.Background(), sdk.CleanOptions{Components: nil,
			DryRun: false})
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("the clean did not wait for the lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	release()
	require.NoError(t, <-done)
	assert.NoDirExists(t, filepath.Join(dir, "_build", "component"))
}
