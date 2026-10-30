package distbuild

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/manifest"
)

// fakeGo answers go list -m -json and go mod edit -json from tables.
type fakeGo struct {
	// modules maps "module@query" to what go list reports.
	modules map[string]moduleInfo
	// gomods maps a go.mod path to its contents.
	gomods map[string]goModFile
	// calls records the commands run.
	calls []string
}

func (f *fakeGo) Output(_ context.Context, _ string, _ []string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))

	switch {
	case len(args) == 4 && args[0] == "list":
		info, ok := f.modules[args[3]]
		if !ok {
			return nil, sdk.Errorf(sdk.ExitFailure, "go list: %s: no matching versions", args[3])
		}

		return json.Marshal(info)
	case len(args) == 4 && args[0] == "mod":
		file, ok := f.gomods[args[3]]
		if !ok {
			return nil, sdk.Errorf(sdk.ExitFailure, "go mod edit: open %s: no such file", args[3])
		}

		return json.Marshal(file)
	}

	return nil, errors.New("unexpected go " + strings.Join(args, " "))
}

func (f *fakeGo) Run(context.Context, string, []string, ...string) error {
	return nil
}

const (
	coreModule = "github.com/tarantool/tt/v3"
	coreHash   = "9031d7ad96d956744789412ac3ce7604c70196a4"
	corePseudo = "v3.0.0-20261028190206-9031d7ad96d9"
)

// coreGoMod is the go.mod of the core: its repository's modules replaced
// with their directories, and one replaced outside it.
func coreGoMod() goModFile {
	return goModFile{
		Module: goModVersion{Path: coreModule},
		Go:     "1.27.3",
		Replace: []goModReplace{
			{
				Old: goModVersion{Path: "github.com/tarantool/tt/sdk"},
				New: goModVersion{Path: "./sdk"},
			},
			{
				Old: goModVersion{Path: "github.com/tarantool/tt/modules/aeon"},
				New: goModVersion{Path: "./modules/aeon"},
			},
			{Old: goModVersion{Path: "example.com/outside"}, New: goModVersion{Path: "../outside"}},
			{
				Old: goModVersion{Path: "example.com/fork"},
				New: goModVersion{Path: "example.com/fork2", Version: "v1.0.0"},
			},
		},
	}
}

func newFakeGo() *fakeGo {
	return &fakeGo{
		modules: map[string]moduleInfo{
			coreModule + "@v3": {
				Path: coreModule, Version: corePseudo, GoMod: "/cache/core.mod",
				Origin: &moduleOrigin{Hash: coreHash},
			},
			coreModule + "@" + corePseudo: {
				Path: coreModule, Version: corePseudo, GoMod: "/cache/core.mod",
			},
			"github.com/tarantool/tt/sdk@" + coreHash: {
				Path: "github.com/tarantool/tt/sdk", Version: testRevision,
			},
			"github.com/tarantool/tt/modules/aeon@" + coreHash: {
				Path: "github.com/tarantool/tt/modules/aeon", Version: testRevision,
			},
			"github.com/tarantool/tt/sdk@9031d7ad96d9": {
				Path: "github.com/tarantool/tt/sdk", Version: testRevision,
			},
			"github.com/tarantool/tt/modules/aeon@9031d7ad96d9": {
				Path: "github.com/tarantool/tt/modules/aeon", Version: testRevision,
			},
			"github.com/acme/tt-foo@latest": {Path: "github.com/acme/tt-foo", Version: "v1.3.0"},
			"github.com/acme/tt-foo@v1.2":   {Path: "github.com/acme/tt-foo", Version: "v1.2.5"},
		},
		gomods: map[string]goModFile{
			"/cache/core.mod":               coreGoMod(),
			"/src/tt/go.mod":                coreGoMod(),
			"/src/ee/plugins/mig/go.mod":    {Module: goModVersion{Path: "example.com/ee/mig"}},
			"/src/ee/plugins/broken/go.mod": {Module: goModVersion{Path: "example.com/other"}},
		},
	}
}

func parse(t *testing.T, src string) *manifest.Distribution {
	t.Helper()

	dist, _, err := manifest.ParseDistribution(
		[]byte("manifest_version = \"0.1\"\nmanifest_kind = \"tt-distribution\"\n" + src))
	require.NoError(t, err)
	require.NoError(t, dist.Validate())

	return dist
}

func TestResolveVersionedCore(t *testing.T) {
	t.Parallel()

	gocmd := newFakeGo()
	res := resolver{gocmd: gocmd, dir: "/src/ee"}

	dist := parse(t, `[core]
tt = "v3"

[dependencies]
aeon = "github.com/tarantool/tt/modules/aeon"
foo = "github.com/acme/tt-foo@v1.2"
latest = { module = "github.com/acme/tt-foo", package = "github.com/acme/tt-foo/cmd" }
mig = "./plugins/mig"
`)

	resolved, err := res.resolveCore(t.Context(), dist)
	require.NoError(t, err)

	assert.Equal(t, core{
		Module: coreModule, Version: corePseudo, Revision: coreHash, GoVersion: "1.27.3",
		Repository: []repoModule{
			{Module: "github.com/tarantool/tt/modules/aeon", Version: testRevision},
			{Module: "github.com/tarantool/tt/sdk", Version: testRevision},
		},
	}, resolved)

	modules, err := res.resolveModules(t.Context(), dist, resolved)
	require.NoError(t, err)

	assert.Equal(t, []module{
		{
			Name: "aeon", Module: "github.com/tarantool/tt/modules/aeon",
			Package: "github.com/tarantool/tt/modules/aeon", Symbol: "New",
		},
		{
			Name: "foo", Module: "github.com/acme/tt-foo", Version: "v1.2.5",
			Package: "github.com/acme/tt-foo", Symbol: "New",
		},
		{
			Name: "latest", Module: "github.com/acme/tt-foo", Version: "v1.3.0",
			Package: "github.com/acme/tt-foo/cmd", Symbol: "New",
		},
		{
			Name: "mig", Module: "example.com/ee/mig", Dir: "/src/ee/plugins/mig",
			Path: "./plugins/mig", Package: "example.com/ee/mig", Symbol: "New",
		},
	}, modules)
}

func TestResolvePinned(t *testing.T) {
	t.Parallel()

	gocmd := newFakeGo()
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
		},
	}

	dist := parse(t, "[core]\ntt = \"v3\"\n[dependencies]\nfoo = \"github.com/acme/tt-foo@v1.2\"\n")

	resolved, err := res.resolveCore(t.Context(), dist)
	require.NoError(t, err)
	assert.Equal(t, corePseudo, resolved.Version)

	modules, err := res.resolveModules(t.Context(), dist, resolved)
	require.NoError(t, err)
	assert.Equal(t, "v1.2.0", modules[0].Version)

	// The locked versions are not queried again.
	assert.Equal(t, []string{
		"list -m -json " + coreModule + "@" + corePseudo,
		"mod edit -json /cache/core.mod",
	}, gocmd.calls)
}

func TestResolveLocalCore(t *testing.T) {
	t.Parallel()

	res := resolver{gocmd: newFakeGo(), dir: "/src/ee"}

	resolved, err := res.resolveCore(t.Context(), parse(t, "[core]\npath = \"../tt\"\n"))
	require.NoError(t, err)

	assert.Equal(t, core{
		Module: coreModule, Dir: "/src/tt", Path: "../tt", GoVersion: "1.27.3",
		Repository: []repoModule{
			{Module: "example.com/outside", Dir: "/src/outside"},
			{Module: "github.com/tarantool/tt/modules/aeon", Dir: "/src/tt/modules/aeon"},
			{Module: "github.com/tarantool/tt/sdk", Dir: "/src/tt/sdk"},
		},
	}, resolved)

	_, err = res.resolveCore(t.Context(),
		parse(t, "[core]\npath = \"../tt\"\nmodule = \"github.com/acme/tt/v3\"\n"))
	require.ErrorContains(t, err, "set [core].module")
}

func TestResolveRunningCore(t *testing.T) {
	t.Parallel()

	res := resolver{gocmd: newFakeGo(), dir: "/src/ee", runningCore: corePseudo}

	resolved, err := res.resolveCore(t.Context(), parse(t, ""))
	require.NoError(t, err)
	assert.Equal(t, corePseudo, resolved.Version)
	// The revision is taken from the pseudo-version when the module source
	// does not report it.
	assert.Equal(t, "9031d7ad96d9", resolved.Revision)

	for _, running := range []string{"", corePseudo + "+dirty"} {
		res.runningCore = running

		_, err = res.resolveCore(t.Context(), parse(t, ""))
		require.ErrorContains(t, err, "set [core].tt or [core].path")
		assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))
	}
}

func TestResolveModuleErrors(t *testing.T) {
	t.Parallel()

	res := resolver{gocmd: newFakeGo(), dir: "/src/ee"}

	dist := parse(t, "[core]\ntt = \"v3\"\n")
	resolved, err := res.resolveCore(t.Context(), dist)
	require.NoError(t, err)

	cases := map[string]string{
		"repository module with a version": "aeon = \"github.com/tarantool/tt/modules/aeon@v1\"\n",
		"no such version":                  "foo = \"github.com/acme/tt-foo@v9\"\n",
		"other module in path": "b = { path = \"./plugins/broken\", " +
			"module = \"example.com/b\" }\n",
		"package of another module": "m = { path = \"./plugins/mig\", " +
			"package = \"example.com/x\" }\n",
		"no go.mod": "m = \"./plugins/none\"\n",
	}

	for name, deps := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := res.resolveModules(t.Context(), parse(t, "[dependencies]\n"+deps), resolved)
			require.Error(t, err)
			assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))
			assert.Contains(t, err.Error(), "dependencies.")
		})
	}
}

func TestRevision(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "9031d7ad96d9", revision(moduleInfo{Version: corePseudo}))
	assert.Equal(t, "abcdef123456",
		revision(moduleInfo{Version: "v3.1.1-0.20260925120000-abcdef123456"}))
	assert.Equal(t, "abcdef123456",
		revision(moduleInfo{Version: "v3.1.0-rc1.0.20260925120000-abcdef123456"}))
	assert.Empty(t, revision(moduleInfo{Version: "v3.1.0"}))
}

func TestGoError(t *testing.T) {
	t.Parallel()

	err := goError([]string{"list"}, errors.New("exit status 1"),
		"go: example.com/x@v1: dial tcp: lookup proxy.golang.org: no such host")
	assert.Equal(t, sdk.ExitSystem, sdk.ExitCode(err))

	err = goError([]string{"list"}, errors.New("exit status 1"),
		"go: example.com/x@v9: no matching versions for query \"v9\"")
	assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))
	assert.Contains(t, err.Error(), "no matching versions")
}

func TestOverrideCore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "v3"), 0o755))

	base := manifest.DistributionCore{Tt: "v3.1.0", Module: "github.com/acme/tt/v3"}

	for value, want := range map[string]manifest.DistributionCore{
		"master":   {Tt: "master", Module: "github.com/acme/tt/v3"},
		"9031d7ad": {Tt: "9031d7ad", Module: "github.com/acme/tt/v3"},
		// An existing directory is a checkout, whatever it is named.
		"v3":      {Path: filepath.Join(dir, "v3"), Module: "github.com/acme/tt/v3"},
		"../tt":   {Path: filepath.Join(filepath.Dir(dir), "tt"), Module: "github.com/acme/tt/v3"},
		"./none":  {Path: filepath.Join(dir, "none"), Module: "github.com/acme/tt/v3"},
		"/src/tt": {Path: "/src/tt", Module: "github.com/acme/tt/v3"},
	} {
		assert.Equal(t, want, overrideCore(base, value, dir), value)
	}
}

func TestResolveOverriddenCore(t *testing.T) {
	t.Parallel()

	gocmd := newFakeGo()
	res := resolver{
		gocmd: gocmd, dir: "/src/ee", unpinnedCore: true,
		pinned: &manifest.DistributionLock{
			Core: manifest.LockedCore{
				Module: coreModule, Version: "v3.0.0-20200101000000-000000000000",
				Repository: map[string]string{"github.com/tarantool/tt/sdk": "v0.0.0-old"},
			},
			Modules: map[string]manifest.LockedModule{
				"foo": {Module: "github.com/acme/tt-foo", Version: "v1.2.0"},
			},
		},
	}

	dist := parse(t, "[dependencies]\nfoo = \"github.com/acme/tt-foo@v1.2\"\n")

	dist.Core = overrideCore(dist.Core, "v3", "/nonexistent")

	resolved, err := res.resolveCore(t.Context(), dist)
	require.NoError(t, err)
	// The core and its repository are resolved again...
	assert.Equal(t, corePseudo, resolved.Version)
	assert.Equal(t, testRevision, resolved.Repository[1].Version)

	// ...and the modules still come from the lock.
	modules, err := res.resolveModules(t.Context(), dist, resolved)
	require.NoError(t, err)
	assert.Equal(t, "v1.2.0", modules[0].Version)
}
