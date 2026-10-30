package distbuild

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/manifest"
)

var update = flag.Bool("update", false, "rewrite golden files")

const testRevision = "v0.0.0-20261028190206-9031d7ad96d9"

// versionedCore is the core of tt at a commit, with its repository.
func versionedCore() core {
	return core{
		Module:    "github.com/tarantool/tt/v3",
		Version:   "v3.0.0-20261028190206-9031d7ad96d9",
		Revision:  "9031d7ad96d956744789412ac3ce7604c70196a4",
		GoVersion: "1.27.3",
		Repository: []repoModule{
			{Module: "github.com/tarantool/tt/modules/aeon", Version: testRevision},
			{Module: "github.com/tarantool/tt/sdk", Version: testRevision},
			{Module: "github.com/tarantool/tt/sdk/connect", Version: testRevision},
		},
	}
}

// testModules are a module of the core's repository, a local one and one
// from a module proxy.
func testModules() []module {
	return []module{
		{
			Name: "aeon", Module: "github.com/tarantool/tt/modules/aeon",
			Package: "github.com/tarantool/tt/modules/aeon", Symbol: "New",
		},
		{
			Name: "foo-bar", Module: "github.com/acme/tt-foo", Version: "v1.2.0",
			Package: "github.com/acme/tt-foo/cmd", Symbol: "Make",
		},
		{
			Name: "migrations", Module: "example.com/ee/plugins/migrations",
			Dir: "/src/ee/plugins/migrations", Path: "./plugins/migrations",
			Package: "example.com/ee/plugins/migrations", Symbol: "New",
		},
	}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", name)

	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}

func TestGenerateGoModVersionedCore(t *testing.T) {
	t.Parallel()

	got, err := generateGoMod(versionedCore(), testModules())
	require.NoError(t, err)
	golden(t, "versioned.go.mod.golden", got)
}

func TestGenerateGoModLocalCore(t *testing.T) {
	t.Parallel()

	local := core{
		Module:    "github.com/tarantool/tt/v3",
		Dir:       "/src/tt",
		Path:      "../tt",
		GoVersion: "1.27.3",
		Repository: []repoModule{
			{Module: "github.com/tarantool/tt/modules/aeon", Dir: "/src/tt/modules/aeon"},
			{Module: "github.com/tarantool/tt/sdk", Dir: "/src/tt/sdk"},
		},
	}

	// The local aeon takes the place of the core's.
	modules := []module{{
		Name: "aeon", Module: "github.com/tarantool/tt/modules/aeon",
		Dir: "/work/aeon dev", Path: "../aeon dev",
		Package: "github.com/tarantool/tt/modules/aeon", Symbol: "New",
	}}

	got, err := generateGoMod(local, modules)
	require.NoError(t, err)
	golden(t, "local.go.mod.golden", got)
}

func TestGenerateGoModConflict(t *testing.T) {
	t.Parallel()

	modules := []module{
		{Name: "a", Module: "github.com/acme/tt-foo", Version: "v1.2.0"},
		{Name: "b", Module: "github.com/acme/tt-foo", Version: "v1.3.0"},
	}

	_, err := generateGoMod(versionedCore(), modules)
	require.ErrorContains(t, err, "listed twice")
	assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))

	// Two packages of one module at one version are fine.
	modules[1].Version = "v1.2.0"
	_, err = generateGoMod(versionedCore(), modules)
	require.NoError(t, err)
}

func TestGenerateMainFull(t *testing.T) {
	t.Parallel()

	got, err := generateMain(mainSpec{
		CoreModule: "github.com/tarantool/tt/v3",
		Modules:    testModules(),
		Options: manifest.DistributionOptions{
			Title:           "Tarantool CLI EE",
			Edition:         "ee",
			Integrity:       manifest.IntegrityRSA,
			ExternalModules: new(false),
		},
		Version: versionInfo{
			Tag: "v2.15.2-3-g9ce7070", Commit: "9ce7070", CommitsSinceTag: 3, Label: "nightly",
		},
	})
	require.NoError(t, err)
	golden(t, "full.main.go.golden", got)
}

func TestGenerateMainMinimal(t *testing.T) {
	t.Parallel()

	got, err := generateMain(mainSpec{CoreModule: "github.com/tarantool/tt/v3"})
	require.NoError(t, err)
	golden(t, "minimal.main.go.golden", got)
}

func TestGenerateMainWithoutBuiltin(t *testing.T) {
	t.Parallel()

	got, err := generateMain(mainSpec{
		CoreModule: "github.com/tarantool/tt/v3",
		Modules: []module{
			{Name: "builtin", Package: "github.com/acme/builtin", Symbol: "New"},
			{Name: "x-y", Package: "github.com/acme/x", Symbol: "New"},
			{Name: "x_y", Package: "github.com/acme/y", Symbol: "New"},
		},
		Options: manifest.DistributionOptions{Builtin: new(false)},
	})
	require.NoError(t, err)
	golden(t, "nobuiltin.main.go.golden", got)
}

func TestPlaceholderVersion(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "v3.0.0-00010101000000-000000000000",
		placeholderVersion("github.com/tarantool/tt/v3"))
	assert.Equal(t, "v12.0.0-00010101000000-000000000000",
		placeholderVersion("example.com/m/v12"))
	assert.Equal(t, "v0.0.0-00010101000000-000000000000",
		placeholderVersion("github.com/tarantool/tt/sdk"))
	assert.Equal(t, "v0.0.0-00010101000000-000000000000",
		placeholderVersion("example.com/v1"))
}
