package core

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	sdkbuild "github.com/tarantool/tt/sdk/build"
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
// with their contents; version.lua, which has the time of the build, is
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

				if entry.Name() == "version.lua" || entry.Name() == "rock_manifest" {
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
		Product: "", Component: "calc", Locked: true, Args: []string{"--help"},
	})
	require.NoError(t, err)
	assert.IsType(t, wasmBuild{}, bld)
	assert.Equal(t, sdkbuild.Component{
		Name: "calc", Path: "components/calc", Namespace: "app", Package: "app", Expects: nil,
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
