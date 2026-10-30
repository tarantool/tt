package distbuild

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/manifest"
)

const replaceManifest = `[core]
tt = "v3"

[dependencies]
aeon = "github.com/tarantool/tt/modules/aeon"
foo = "github.com/acme/tt-foo@v1.2"
mig = "./plugins/mig"

[replace]
"example.com/migration" = "../migration"
"github.com/tarantool/go-storage/v2" = "github.com/tarantool/go-storage/v2@main"
"example.com/old@v1.2.3" = "/abs/old"
`

// replaceFakeGo is newFakeGo with the modules the replacements name.
func replaceFakeGo() *fakeGo {
	gocmd := newFakeGo()

	gocmd.modules["github.com/tarantool/go-storage/v2@main"] = moduleInfo{
		Path: "github.com/tarantool/go-storage/v2", Version: "v2.1.0-0.20261001000000-abcdef123456",
	}
	gocmd.modules["github.com/tarantool/go-storage/v2@v2.1.0-0.20261001000000-abcdef123456"] =
		gocmd.modules["github.com/tarantool/go-storage/v2@main"]

	for path, module := range map[string]string{
		"/src/migration/go.mod": "example.com/migration",
		"/abs/old/go.mod":       "example.com/old",
		"/src/ee/wrong/go.mod":  "example.com/else",
	} {
		gocmd.gomods[path] = goModFile{Module: goModVersion{Path: module}}
	}

	return gocmd
}

func resolveForReplace(
	t *testing.T, res resolver, dist *manifest.Distribution,
) ([]replacement, error) {
	t.Helper()

	resolved, err := res.resolveCore(t.Context(), dist)
	require.NoError(t, err)

	modules, err := res.resolveModules(t.Context(), dist, resolved)
	require.NoError(t, err)

	return res.resolveReplaces(t.Context(), dist, resolved, modules)
}

func TestResolveReplaces(t *testing.T) {
	t.Parallel()

	res := resolver{gocmd: replaceFakeGo(), dir: "/src/ee"}

	got, err := resolveForReplace(t, res, parse(t, replaceManifest))
	require.NoError(t, err)

	assert.Equal(t, []replacement{
		{
			Key: "example.com/migration", Module: "example.com/migration",
			Dir: "/src/migration", Path: "../migration",
		},
		{
			Key: "example.com/old@v1.2.3", Module: "example.com/old", Version: "v1.2.3",
			Dir: "/abs/old", Path: "/abs/old",
		},
		{
			Key:    "github.com/tarantool/go-storage/v2",
			Module: "github.com/tarantool/go-storage/v2",
			Target: "github.com/tarantool/go-storage/v2",
			// A query is resolved to the exact version.
			TargetVersion: "v2.1.0-0.20261001000000-abcdef123456",
		},
	}, got)
}

func TestResolveReplacesPinned(t *testing.T) {
	t.Parallel()

	gocmd := replaceFakeGo()
	res := resolver{
		gocmd: gocmd, dir: "/src/ee",
		pinned: &manifest.DistributionLock{
			Core: manifest.LockedCore{
				Module: coreModule, Version: corePseudo, Revision: coreHash,
				Repository: map[string]string{
					"github.com/tarantool/tt/sdk":          testRevision,
					"github.com/tarantool/tt/modules/aeon": testRevision,
				},
			},
			Modules: map[string]manifest.LockedModule{
				"foo": {Module: "github.com/acme/tt-foo", Version: "v1.2.0"},
			},
			Replace: map[string]manifest.LockedReplace{
				"github.com/tarantool/go-storage/v2": {
					Module:  "github.com/tarantool/go-storage/v2",
					Version: "v2.0.5",
				},
			},
		},
	}

	got, err := resolveForReplace(t, res, parse(t, replaceManifest))
	require.NoError(t, err)

	assert.Equal(t, "v2.0.5", got[2].TargetVersion)

	// The locked version is not queried again.
	for _, call := range gocmd.calls {
		assert.NotContains(t, call, "go-storage")
	}
}

func TestResolveReplacesErrors(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		replace string
		want    string
	}{
		"module of the core's repository": {
			`"github.com/tarantool/tt/sdk" = "../sdk"`,
			"is a module of the core's repository and follows the core: " +
				"use [core].path or --core",
		},
		"module of a local tt module": {
			`"example.com/ee/mig" = "../mig2"`,
			`is the tt module "mig" of [dependencies]`,
		},
		"directory of another module": {
			`"example.com/migration" = "./wrong"`,
			"./wrong holds module example.com/else, not example.com/migration",
		},
		"no go.mod": {
			`"example.com/migration" = "./none"`, "no such file",
		},
		"no such version": {
			`"example.com/migration" = "example.com/migration@v9"`, "no matching versions",
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			res := resolver{gocmd: replaceFakeGo(), dir: "/src/ee"}

			_, err := resolveForReplace(t, res, parse(t, `[core]
tt = "v3"

[dependencies]
mig = "./plugins/mig"
foo = "github.com/acme/tt-foo@v1.2"

[replace]
`+testCase.replace+"\n"))
			require.ErrorContains(t, err, testCase.want)
			assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))
			assert.Contains(t, err.Error(), "replace.")
		})
	}
}

func TestGenerateGoModReplace(t *testing.T) {
	t.Parallel()

	replaces := []replacement{
		{
			Key: "example.com/migration", Module: "example.com/migration",
			Dir: "/work/migration dev", Path: "../migration dev",
		},
		{
			Key: "example.com/old@v1.2.3", Module: "example.com/old", Version: "v1.2.3",
			Dir: "/abs/old", Path: "/abs/old",
		},
		{
			Key: "example.com/old", Module: "example.com/old",
			Target: "example.com/old2", TargetVersion: "v1.0.0",
		},
		{
			Key:    "github.com/tarantool/go-storage/v2",
			Module: "github.com/tarantool/go-storage/v2",
			Target: "github.com/tarantool/go-storage/v2", TargetVersion: "v2.1.0-rc1",
		},
	}

	got, err := generateGoMod(versionedCore(), testModules(), replaces)
	require.NoError(t, err)
	golden(t, "replace.go.mod.golden", got)

	// The same replacements are the same file.
	again, err := generateGoMod(versionedCore(), testModules(), replaces)
	require.NoError(t, err)
	assert.Equal(t, string(got), string(again))
}

func TestGenerateGoModReplaceConflict(t *testing.T) {
	t.Parallel()

	// The build replaces the modules of a local core by itself.
	local := core{
		Module: "github.com/tarantool/tt/v3", Dir: "/src/tt", GoVersion: "1.27.3",
		Repository: []repoModule{{Module: "github.com/tarantool/tt/sdk", Dir: "/src/tt/sdk"}},
	}

	_, err := generateGoMod(local, nil, []replacement{{
		Key: "github.com/tarantool/tt/sdk", Module: "github.com/tarantool/tt/sdk", Dir: "/x",
	}})
	require.ErrorContains(t, err, "is replaced by the build already")
	assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))
}

// writeModule writes a module with one file in dir.
func writeModule(t *testing.T, dir, content string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/m\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "m.go"), []byte(content), 0o644))
}

func TestReplaceContentHashes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "m")
	writeModule(t, dir, "package m\n")

	replaces := []replacement{
		{Key: "example.com/m", Module: "example.com/m", Dir: dir, Path: "./m"},
		{
			Key: "example.com/n", Module: "example.com/n",
			Target: "example.com/n2", TargetVersion: "v1.0.0",
		},
	}

	bld := builder{work: filepath.Join(root, "work")}

	hashes, err := bld.hashLocal(core{}, nil, replaces)
	require.NoError(t, err)

	// Only a directory has a content hash.
	require.Len(t, hashes.replaces, 1)
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, hashes.replaces["example.com/m"])

	lock := &manifest.DistributionLock{
		Replace: map[string]manifest.LockedReplace{
			"example.com/m": {Path: "./m", ContentHash: hashes.replaces["example.com/m"]},
		},
	}

	bld.lock = lock
	require.NoError(t, bld.checkContentHashes(core{}, nil, replaces, hashes))

	// A changed directory fails a build from the lock.
	writeModule(t, dir, "package m\n\nconst changed = true\n")

	changed, err := bld.hashLocal(core{}, nil, replaces)
	require.NoError(t, err)

	err = bld.checkContentHashes(core{}, nil, replaces, changed)
	require.ErrorIs(t, err, errLocalChanged)
	require.ErrorContains(t, err, "./m (replace example.com/m) changed since tt.manifest.lock")
	assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))

	// A directory the lock does not know is a change as well.
	bld.lock = &manifest.DistributionLock{}
	require.ErrorIs(t, bld.checkContentHashes(core{}, nil, replaces, hashes), errLocalChanged)
}

func TestLocalizeReplaces(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "m")
	writeModule(t, dir, "package m\n")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("x"), 0o644))

	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o755))

	replaces := []replacement{
		{Key: "example.com/m", Module: "example.com/m", Dir: dir, Path: "../m"},
		{
			Key: "example.com/m@v1.0.0", Module: "example.com/m", Version: "v1.0.0",
			Dir: dir, Path: "../m",
		},
		{
			Key: "example.com/n", Module: "example.com/n",
			Target: "example.com/n2", TargetVersion: "v1.0.0",
		},
	}

	bld := builder{work: work}

	_, _, localized, err := bld.localize(core{}, nil, replaces)
	require.NoError(t, err)

	assert.Equal(t, "./local/replace/example.com/m", localized[0].Dir)
	assert.Equal(t, "./local/replace/example.com/m_v1.0.0", localized[1].Dir)
	assert.Empty(t, localized[2].Dir)
	assert.FileExists(t, filepath.Join(work, "local", "replace", "example.com", "m", "m.go"))
	assert.NoDirExists(t, filepath.Join(work, "local", "replace", "example.com", "m", ".git"))

	// The input is left as it was: the lock records the manifest's paths.
	assert.Equal(t, dir, replaces[0].Dir)

	gomod, err := generateGoMod(versionedCore(), nil, localized)
	require.NoError(t, err)
	assert.Contains(t, string(gomod), "example.com/m => ./local/replace/example.com/m\n")
	assert.Contains(t, string(gomod),
		"example.com/m v1.0.0 => ./local/replace/example.com/m_v1.0.0\n")
	assert.NotContains(t, string(gomod), root)
}
