package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// errVendorFailed is what the Vendor of fetch fails with when told to.
var errVendorFailed = errors.New("the download failed")

// fetchConfig is the configuration of the fetch backend.
type fetchConfig struct {
	// Module is the module the build makes.
	Module string `toml:"module"`
	// FailVendor has the Vendor of the build fail.
	FailVendor bool `toml:"fail_vendor"`
}

// fetchRecords records the Contexts the builds and the Vendor of fetch ran
// in, by component.
type fetchRecords struct {
	mu      sync.Mutex
	builds  map[string]sdkbuild.Context
	vendors map[string]sdkbuild.Context
}

// fetchBuild is a checked block of fetch: it makes a module, and its Vendor
// writes a file of its component into the directory it is given.
type fetchBuild struct {
	config  fetchConfig
	records *fetchRecords
}

var _ sdkbuild.Vendorer = fetchBuild{}

// record records facts in contexts.
func (r *fetchRecords) record(contexts map[string]sdkbuild.Context, facts *sdkbuild.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()

	contexts[facts.Component.Name] = *facts
}

// Run makes the module of the block and records its Context.
func (b fetchBuild) Run(_ context.Context, facts *sdkbuild.Context) error {
	b.records.record(b.records.builds, facts)

	return os.WriteFile(filepath.Join(facts.Dirs.Modules, b.config.Module), []byte("so"), 0o600)
}

// Vendor writes <dir>/<component>, which says whether the Context is
// locked, and records the Context.
func (b fetchBuild) Vendor(_ context.Context, facts *sdkbuild.Context, dir string) error {
	b.records.record(b.records.vendors, facts)

	if b.config.FailVendor {
		return errVendorFailed
	}

	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, facts.Component.Name),
		[]byte(strconv.FormatBool(facts.Locked)), 0o600)
}

// fetchBackend is the fetch backend, whose builds record into records.
func fetchBackend(records *fetchRecords) sdkbuild.Backend {
	return sdkbuild.Backend{
		Name:     "fetch",
		Produces: []sdkbuild.Result{sdkbuild.Module},
		Config:   func() any { return new(fetchConfig) },
		New: func(cfg any, _ sdkbuild.Component, _ sdkbuild.Common) (sdkbuild.Build, error) {
			config, ok := cfg.(*fetchConfig)
			if !ok {
				return nil, sdkbuild.FieldErrorf("", "not a configuration of fetch")
			}

			return fetchBuild{config: *config, records: records}, nil
		},
		Incremental:    false,
		OutputFrom:     sdkbuild.OutputNone,
		Exclude:        nil,
		NoSourceLayout: false,
		Hook:           false,
	}
}

// fetchManifest is my-app with two components of fetch, the second of a
// product of its own, and one of exe, which downloads nothing ahead.
const fetchManifest = manifestHead + `
[components.zeta]
path = 'zeta'

[components.zeta.build]
backend = 'fetch'
module = 'zeta.so'

[components.alpha]
path = 'alpha'

[components.alpha.build]
backend = 'fetch'
module = 'alpha.so'

[components.gw]
path = 'gw'

[components.gw.build]
backend = 'exe'

[products.default]
components = ['lua', 'zeta', 'gw']
default = true

[products.extra]
components = ['alpha']
rock = 'my-app-extra'
`

// fetchProject writes a project of manifest and returns its directory, the
// options of its builds and what fetch records.
func fetchProject(t *testing.T, manifest string) (string, Options, *fetchRecords) {
	t.Helper()

	dir := setupProject(t, manifest, map[string]string{
		"init.lua":       "return {}\n",
		"zeta/init.lua":  "return {}\n",
		"alpha/init.lua": "return {}\n",
	})

	records := &fetchRecords{
		mu: sync.Mutex{}, builds: map[string]sdkbuild.Context{},
		vendors: map[string]sdkbuild.Context{},
	}

	backends := buildregtest.Registry()
	require.NoError(t, backends.Add("fetch", []sdkbuild.Backend{fetchBackend(records)}, nil))

	opts := dryOptions(dir)

	opts.Backends = backends

	return dir, opts, records
}

// TestVendor runs the Vendor of every component of a Vendorer, of every
// product, in name order, into the directory of its backend in the mirror,
// in a locked Context of a build of the project; a component whose build is
// no Vendorer downloads nothing.
func TestVendor(t *testing.T) {
	t.Parallel()

	dir, opts, records := fetchProject(t, fetchManifest)
	mirror := filepath.Join(t.TempDir(), "mirror")

	result, err := Vendor(context.Background(), opts, mirror)
	require.NoError(t, err)

	assert.Equal(t, []string{"alpha", "zeta"}, result.Components)
	assert.Equal(t, []string{filepath.Join(mirror, "fetch")}, result.Dirs)

	for _, name := range []string{"alpha", "zeta"} {
		data, err := os.ReadFile(filepath.Join(mirror, "fetch", name))
		require.NoError(t, err)
		assert.Equal(t, "true", string(data), "the Context of %s is locked", name)
		assert.Equal(t, filepath.Join(dir, name), records.vendors[name].Dirs.Component)
	}

	assert.NotContains(t, records.vendors, "gw")
	assert.Empty(t, records.builds, "nothing is built")
}

// TestVendor_pathDependency runs the Vendor of the components of a path
// dependency the lock pins too, in the Context its build by the consumer
// has, after those of the project.
func TestVendor_pathDependency(t *testing.T) {
	t.Parallel()

	app, contrib := setupNeighbours(t)
	mirror := filepath.Join(t.TempDir(), "mirror")

	withFetched := strings.NewReplacer(
		"[products.default]", `[components.fetched]
path = 'fetched'

[components.fetched.build]
backend = 'fetch'
module = 'fetched.so'

[products.default]`,
		"components = ['lua', 'native']", "components = ['lua', 'native', 'fetched']",
	).Replace(contribManifest)

	writeFile(t, filepath.Join(contrib, manifestFileName), withFetched)
	writeFile(t, filepath.Join(contrib, "fetched", "init.lua"), "return {}\n")

	records := &fetchRecords{
		mu: sync.Mutex{}, builds: map[string]sdkbuild.Context{},
		vendors: map[string]sdkbuild.Context{},
	}

	backends := buildregtest.Registry()
	require.NoError(t, backends.Add("fetch", []sdkbuild.Backend{fetchBackend(records)}, nil))

	opts := dryOptions(app)

	opts.Backends = backends

	// The lock the download needs, which pins the path dependency.
	require.NoError(t, Run(context.Background(), opts))

	result, err := Vendor(context.Background(), opts, mirror)
	require.NoError(t, err)

	assert.Equal(t, []string{"contrib/fetched"}, result.Components)
	assert.Equal(t, filepath.Join(contrib, "fetched"), records.vendors["fetched"].Dirs.Component)
	assert.Equal(t, filepath.Join(app, buildDirName, pathDirName, "contrib"),
		records.vendors["fetched"].Dirs.State)
	assert.FileExists(t, filepath.Join(mirror, "fetch", "fetched"))
}

// TestVendor_failing fails the download with exit code 2 when a Vendor
// fails, naming the component.
func TestVendor_failing(t *testing.T) {
	t.Parallel()

	_, opts, _ := fetchProject(t, manifestHead+`
[components.bad]
path = 'bad'

[components.bad.build]
backend = 'fetch'
module = 'bad.so'
fail_vendor = true
`)

	_, err := Vendor(context.Background(), opts, t.TempDir())
	require.ErrorIs(t, err, errVendorFailed)
	assert.Contains(t, err.Error(), `downloading what component "bad" builds with`)
	assert.Equal(t, sdk.ExitSystem, exitcode.Code(err))
}

// TestRun_vendoredFromAMirror: a build whose rock servers have a mirror
// with a directory of the backend is given it, the first one in their
// order; one whose servers have none is given none.
func TestRun_vendoredFromAMirror(t *testing.T) {
	t.Parallel()

	dir, opts, records := fetchProject(t, fetchManifest)

	require.NoError(t, Run(context.Background(), opts))
	assert.Empty(t, records.builds["zeta"].Vendored, "no mirror among the rock servers")

	empty, first, second := t.TempDir(), t.TempDir(), t.TempDir()

	for _, mirror := range []string{first, second} {
		require.NoError(t, os.MkdirAll(filepath.Join(mirror, "fetch"), 0o750))
	}

	opts.Registries = rocks.Sources{
		Flag: []string{empty, first, second}, Env: nil, Manifest: nil, WorkingDir: dir,
		ProjectDir: dir,
	}

	require.NoError(t, Run(context.Background(), opts))
	assert.Equal(t, filepath.Join(first, "fetch"), records.builds["zeta"].Vendored)

	// The source rock of a server is built with no mirror: tt package
	// download did not fill it for the rock.
	proj := packageProject(dir, readTestManifest(t, dir), "default")

	assert.Equal(t, []string{empty, first, second}, localRegistries(opts, proj))

	proj.fromServer = true

	assert.Empty(t, localRegistries(opts, proj))
}

// readTestManifest reads the manifest of the project in dir.
func readTestManifest(t *testing.T, dir string) *manifest.Manifest {
	t.Helper()

	man, err := readManifest(dir, dryOptions(dir))
	require.NoError(t, err)

	return man
}
