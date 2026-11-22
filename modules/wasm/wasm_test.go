package wasm_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/modules/wasm"
	"github.com/tarantool/tt/sdk/v3"
	"github.com/tarantool/tt/sdk/v3/sdktest"
)

// defaultRockServer is the rock server prepare declares the rock from by
// default.
const defaultRockServer = "https://raw.githubusercontent.com/tarantool-contrib/rocks-repo/index/"

// testProject returns a project directory with a manifest, which prepare
// looks for before it declares anything.
func testProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.manifest.toml"),
		[]byte("manifest_version = \"0.1\"\n"), 0o644))

	return dir
}

// declaring is a manifest that declares the wasm rock from registry.
func declaring(registry string) sdk.Manifest {
	return sdk.Manifest{
		Dependencies: map[string]sdk.ManifestDependency{
			"wasm": {Version: "0.1.0", Registry: registry, Path: ""},
		},
		DevDependencies: nil,
		Components: map[string]sdk.ManifestComponent{
			"app":    {Path: "app", Build: nil},
			"router": {Path: "router", Build: &sdk.ManifestBuild{Backend: "shell", Cwd: ""}},
		},
	}
}

// withTarantool is a tt with a Tarantool.
func withTarantool() sdktest.Option {
	return sdktest.WithTarantool("/usr/bin/tarantool",
		sdk.TarantoolVersion{Major: 3, Minor: 8, Patch: 1})
}

// TestPrepareDeclaresTheRock checks that tt wasm prepare declares the rock
// from the default rock server through the project of the SDK, with the
// --registry servers, prints how the lock moved before the line on the
// declaration and what it prepared after it, and writes the scripts of
// .env.
func TestPrepareDeclaresTheRock(t *testing.T) {
	t.Parallel()

	project := testProject(t)
	services := sdktest.New(t, sdktest.WithRoot(project), withTarantool(),
		sdktest.WithMoves(sdk.DependencyMove{Name: "wasm", From: "", To: "0.1.0-1"}))

	got := services.RunModule(wasm.New, "wasm", "prepare", "--no-tarawasm",
		"--registry", "mirror")
	require.NoError(t, got.Err)

	assert.Equal(t, []sdk.Dependency{{
		Name: "wasm", Constraint: "0.1.0", Registry: defaultRockServer, Dev: false,
		Registries: []string{"mirror"},
	}}, services.AddedDependencies())
	assert.Equal(t, 1, services.PreparedEnvironments())
	assert.Regexp(t, `^  wasm 0\.1\.0-1\n`+
		`prepared for tarantool-wasm 0\.1\.0 \([a-z]+-[a-z0-9_]+\)\n`+
		`  rock:       from `+defaultRockServer+`\n$`, got.Stdout)
	assert.Equal(t, "declared wasm 0.1.0 from "+defaultRockServer+"\n", got.Stderr)

	// A declaration already there is left as it is, and the lock is
	// resolved against it.
	again := sdktest.New(t, sdktest.WithRoot(project), withTarantool(),
		sdktest.WithManifest(declaring(".tarantool/registry")),
		sdktest.WithMoves(sdk.DependencyMove{Name: "wasm", From: "", To: "0.1.0-1"}))

	got = again.RunModule(wasm.New, "wasm", "prepare", "--no-tarawasm", "--registry", "mirror")
	require.NoError(t, got.Err)
	assert.Equal(t, "wasm is already declared in app.manifest.toml; left as it is\n",
		got.Stderr)
	assert.Regexp(t, `^  wasm 0\.1\.0-1\nprepared for tarantool-wasm 0\.1\.0 `, got.Stdout)
	assert.Empty(t, again.AddedDependencies())
	assert.Equal(t, []sdk.ResolveOptions{{Registries: []string{"mirror"}}}, again.Resolves())

	man, err := again.Project().Manifest()
	require.NoError(t, err)
	assert.Equal(t, ".tarantool/registry", man.Dependencies["wasm"].Registry)
}

// TestPrepareFromIndexesTheRegistry checks that prepare --from packs the
// build into the registry of the project and has the rock servers of the
// SDK index it, keeping a rock index only where there is one.
func TestPrepareFromIndexesTheRegistry(t *testing.T) {
	t.Parallel()

	project := testProject(t)
	build := filepath.Join(t.TempDir(), "libtarantool_wasm_rs.so")
	require.NoError(t, os.WriteFile(build, []byte("\x7fELF module"), 0o644))

	services := sdktest.New(t, sdktest.WithRoot(project), withTarantool())

	got := services.RunModule(wasm.New, "wasm", "prepare", "--no-tarawasm", "--from", build)
	require.NoError(t, got.Err)

	registry := filepath.Join(project, ".tarantool", "registry")

	assert.Equal(t, []sdktest.Index{{
		Dir: registry, Options: sdk.IndexOptions{IfIndexed: true},
	}}, services.Indexed())
	require.Len(t, services.AddedDependencies(), 1)
	assert.Equal(t, ".tarantool/registry", services.AddedDependencies()[0].Registry)
	assert.Contains(t, got.Stdout, "  rock:       .tarantool/registry/wasm-0.1.0-1.")
}

// TestPrepareFailsToDeclare checks that prepare fails at the declaration of
// the rock when the project cannot declare it - a tt with no Tarantool, as
// the core says it - before tarawasm is installed.
func TestPrepareFailsToDeclare(t *testing.T) {
	t.Parallel()

	project := testProject(t)
	services := sdktest.New(t, sdktest.WithRoot(project),
		sdktest.WithAddDependencyError(errors.New("tarantool executable not found in PATH")))

	got := services.RunModule(wasm.New, "wasm", "prepare")
	require.EqualError(t, got.Err,
		"declaring the wasm dependency: tarantool executable not found in PATH")
	assert.Len(t, services.AddedDependencies(), 1)
	assert.NoDirExists(t, filepath.Join(project, ".tarantool"))
	assert.Empty(t, got.Stderr)
}

// TestNewRefusesADeclaredName checks that new refuses a name the manifest
// declares, before tarawasm runs.
func TestNewRefusesADeclaredName(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t, sdktest.WithRoot(testProject(t)), withTarantool(),
		sdktest.WithManifest(declaring(".tarantool/registry")))

	got := services.RunModule(wasm.New, "wasm", "new", "router", "--lang", "python")
	require.EqualError(t, got.Err, "component already declared: router")
	assert.Empty(t, services.AddedComponents())
}

// TestStatus checks what tt wasm status reports of a project the manifest of
// the project of the SDK describes, and of one with no manifest.
func TestStatus(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	services := sdktest.New(t, sdktest.WithRoot(project),
		sdktest.WithManifest(declaring("https://rocks.example.test/")))

	got := services.RunModule(wasm.New, "wasm", "status")
	require.NoError(t, got.Err)
	assert.Equal(t, "wasm:       0.1.0 from https://rocks.example.test/\n"+
		"rock:       from https://rocks.example.test/\n"+
		"tarawasm:   missing\n"+
		"ready\n", got.Stdout)

	none := sdktest.New(t, sdktest.WithRoot(project), sdktest.WithNoManifest())

	got = none.RunModule(wasm.New, "wasm", "status")
	require.EqualError(t, got.Err, "not a tt project: "+project+
		" has no app.manifest.toml (create one with tt new)")
}

// TestRockFrom checks that tt wasm rock --from packs the build as the rock of
// --arch and has the rock servers of the SDK index the directory, with its
// rock index, and prints the rock, the manifest and the index.
func TestRockFrom(t *testing.T) {
	t.Parallel()

	build := filepath.Join(t.TempDir(), "libtarantool_wasm_rs.dylib")
	require.NoError(t, os.WriteFile(build, []byte("module"), 0o644))

	out := filepath.Join(t.TempDir(), "rocks")
	services := sdktest.New(t)

	got := services.RunModule(wasm.New, "wasm", "rock", "--from", build, "--arch", "macosx-aarch64",
		"--out", out)
	require.NoError(t, got.Err)

	assert.Equal(t, []sdktest.Index{{Dir: out, Options: sdk.IndexOptions{IfIndexed: false}}},
		services.Indexed())
	assert.Equal(t, filepath.Join(out, "wasm-0.1.0-1.macosx-aarch64.rock")+"\n"+
		filepath.Join(out, "manifest")+"\n"+
		filepath.Join(out, "tt-rocks-index.json")+"\n", got.Stdout)
	assert.Equal(t, "packing "+build+" as the rock of macosx-aarch64\n", got.Stderr)
}
