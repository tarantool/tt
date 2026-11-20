package wasm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/v3"
	"github.com/tarantool/tt/sdk/v3/sdktest"
)

// testManifest is what the manifest of the package writeProject writes
// declares, as the project of the SDK reads it: the wasm rock from the
// registry of the project, a Lua component and one tarawasm builds.
func testManifest() sdk.Manifest {
	return sdk.Manifest{
		Dependencies: map[string]sdk.ManifestDependency{
			RockName: {Version: "0.1.0", Registry: ".tarantool/registry", Path: ""},
		},
		DevDependencies: map[string]sdk.ManifestDependency{},
		Components: map[string]sdk.ManifestComponent{
			"app": {Path: "app", Build: nil},
			"router": {
				Path: "router", Build: &sdk.ManifestBuild{Backend: "shell", Cwd: ""},
			},
		},
	}
}

// writeProject writes a package with a Lua component and a tarawasm one
// whose SDK lock pins sdkVersion, and returns its directory and what its
// manifest declares (testManifest).
func writeProject(t *testing.T, sdkVersion string) (string, sdk.Manifest) {
	t.Helper()

	project := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(project, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), dirPerm))
		require.NoError(t, os.WriteFile(path, []byte(content), filePerm))
	}

	write("app/init.lua", "return {}\n")
	write("router/tarawasm.json", `{"language": "python", "world": "app", `+
		`"source": "main.py", "output": "dist/app.wasm"}`)
	write("router/tarawasm.tarantool.lock.json", `{"version": "`+sdkVersion+`"}`)

	return project, testManifest()
}

func TestFindComponents(t *testing.T) {
	t.Parallel()

	project, man := writeProject(t, "0.1.0")

	components, err := FindComponents(project, man)
	require.NoError(t, err)
	assert.Equal(t, []Component{{
		Name: "router", Dir: filepath.Join(project, "router"), Language: "python",
		Output: "dist/app.wasm", SDKVersion: "0.1.0",
	}}, components)

	version := DeclaredVersion(man)
	assert.Equal(t, "0.1.0", version)
	assert.Empty(t, SDKMismatches(components, version))

	// The directory of the build of a component is its cwd.
	moved := filepath.Join(project, "router")
	require.NoError(t, os.Rename(moved, filepath.Join(project, "src")))

	man.Components["router"] = sdk.ManifestComponent{
		Path: "router", Build: &sdk.ManifestBuild{Backend: "shell", Cwd: "src"},
	}

	components, err = FindComponents(project, man)
	require.NoError(t, err)
	require.Len(t, components, 1)
	assert.Equal(t, filepath.Join(project, "src"), components[0].Dir)
}

func TestDeclaredVersion(t *testing.T) {
	t.Parallel()

	for constraint, want := range map[string]string{
		"0.1.0": "0.1.0", "==0.2.0": "0.2.0", ">=0.1.0": "", "": "",
	} {
		man := testManifest()

		man.Dependencies[RockName] = sdk.ManifestDependency{
			Version: constraint, Registry: "", Path: "",
		}

		assert.Equal(t, want, DeclaredVersion(man), constraint)
	}

	assert.Empty(t, DeclaredVersion(sdk.Manifest{
		Dependencies: nil, DevDependencies: nil, Components: nil,
	}))
}

func TestSDKMismatch(t *testing.T) {
	t.Parallel()

	project, man := writeProject(t, "0.0.9")

	components, err := FindComponents(project, man)
	require.NoError(t, err)

	mismatches := SDKMismatches(components, "0.1.0")
	require.Len(t, mismatches, 1)
	assert.Equal(t, `component "router" is built with the tarantool-wasm 0.0.9 SDK, `+
		`the package declares wasm 0.1.0`, mismatches[0])
}

func TestComponentLanguages(t *testing.T) {
	t.Parallel()

	components := []Component{{Name: "a", Dir: "", Language: "rust", Output: "", SDKVersion: ""}}
	assert.Equal(t, []string{"python", "rust"}, componentLanguages(components, "python"))
	assert.Equal(t, []string{"rust"}, componentLanguages(components, "rust"))
}

// TestComponentOf checks the component tt wasm new declares: what the
// manifest says of it, and the fields of its build in their order, which
// the core writes as they come.
func TestComponentOf(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t, sdktest.WithManifest(testManifest()))
	services.Start()

	component := componentOf("api", filepath.Join("wasm", "api"), "dist/api.wasm")

	assert.Equal(t, sdk.Component{
		Name:    "api",
		Comment: "A WebAssembly component, made by tt wasm new and built by tarawasm.",
		Path:    "wasm/api",
		Include: []string{"dist/api.wasm"},
		Build: &sdk.ComponentBuild{Backend: "shell", Fields: []sdk.BuildField{
			{Key: "command", Value: "tarawasm"},
			{Key: "args", Value: []string{"all"}},
			{Key: "incremental", Value: true},
		}},
	}, component)

	require.NoError(t, services.Project().AddComponent(t.Context(), component))

	man, err := services.Project().Manifest()
	require.NoError(t, err)
	assert.Equal(t, sdk.ManifestComponent{
		Path: "wasm/api", Include: []string{"dist/api.wasm"}, Exclude: nil, Namespace: "",
		Build: &sdk.ManifestBuild{
			Backend: "shell", Cwd: "", Env: nil, Output: nil, Incremental: true, Static: false,
			Fields: map[string]any{"command": "tarawasm", "args": []any{"all"}},
		},
	}, man.Components["api"])
}

// TestNewComponentChecksTheProject checks what NewComponent refuses before
// it runs anything: a name the manifest declares, a package with no exact
// wasm version, a project tarawasm is missing from, and a manifest that
// cannot be read.
func TestNewComponentChecksTheProject(t *testing.T) {
	t.Parallel()

	project, man := writeProject(t, "0.1.0")

	options := func(man sdk.Manifest, name string) NewOptions {
		return NewOptions{
			ProjectDir: project, Name: name, Dir: "", Language: "python", World: "",
			Template: "", Fetcher: Fetcher{CacheDir: "", Client: nil},
			Tarawasm: Tarawasm{Python: "", Source: "", Reinstall: false, Output: nil, binDir: ""},
			Manifest: func() (sdk.Manifest, error) { return man, nil },
			AddComponent: func(_ context.Context, _ sdk.Component) error {
				t.Fatal("nothing is declared")

				return nil
			},
			Resolve: func(context.Context) error {
				t.Fatal("nothing is resolved")

				return nil
			},
			Progress: nil,
		}
	}

	_, err := NewComponent(t.Context(), options(man, "router"))
	require.ErrorIs(t, err, ErrComponentExists)

	_, err = NewComponent(t.Context(), options(man, "calc"))
	require.ErrorIs(t, err, ErrNoTarawasm)
	assert.Contains(t, err.Error(), filepath.Join(project, ".tarantool", "bin", "tarawasm"))

	undeclared := testManifest()

	delete(undeclared.Dependencies, RockName)

	_, err = NewComponent(t.Context(), options(undeclared, "calc"))
	require.ErrorIs(t, err, ErrNoTarawasm)
	assert.Contains(t, err.Error(), "the package declares no exact wasm version")

	failing := options(man, "calc")

	failing.Manifest = func() (sdk.Manifest, error) {
		return sdk.Manifest{}, NoManifestError(project)
	}

	_, err = NewComponent(t.Context(), failing)
	require.ErrorIs(t, err, ErrNoManifest)
	assert.Equal(t, "not a tt project: "+project+" has no app.manifest.toml "+
		"(create one with tt new)", err.Error())

	broken := errors.New("parsing app.manifest.toml: broken")

	failing.Manifest = func() (sdk.Manifest, error) { return sdk.Manifest{}, broken }

	_, err = NewComponent(t.Context(), failing)
	require.ErrorIs(t, err, broken)
}

func TestGitignore(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "# Generated by tarawasm, and rebuilt by tarawasm all.\n"+
		".tarawasm/\ndist/\n# Where the tools tt installed are, on this machine.\n"+
		"tarawasm.local.json\n__pycache__/\nwit_world/\ncomponentize_py_*\npoll_loop.py\n",
		gitignore("python"))
	assert.Contains(t, gitignore("rust"), "target/\n")
}
