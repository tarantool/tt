package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
)

// zigBuild is the build of the zig backend of a module, whose tool is
// nowhere.
type zigBuild struct{}

func (zigBuild) Preflight(_ context.Context, c *sdkbuild.Context) error {
	_, err := c.LookPath("no-such-zig-of-tt")
	if err != nil {
		return fmt.Errorf("%w: no-such-zig-of-tt", sdkbuild.ErrNoTool)
	}

	return nil
}

func (zigBuild) Run(context.Context, *sdkbuild.Context) error { return nil }

// zigManifest is a project with a component of a backend no module of the
// core provides.
const zigManifest = manifestHead + `
[components.fast]
path = 'fast'

[components.fast.build]
backend = 'zig'
steps = ['install']
`

// TestRun_unknownBackendFailsFirst checks that the build of a component of a
// backend tt has not fails before anything is resolved or built, while the
// manifest itself is only warned of.
func TestRun_unknownBackendFailsFirst(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, zigManifest, map[string]string{"fast/build.zig": ""})

	var warnings []string

	opts := dryOptions(dir)

	opts.Warn = func(warning string) { warnings = append(warnings, warning) }

	err := Run(context.Background(), opts)
	require.ErrorIs(t, err, buildreg.ErrNoBackend)
	require.EqualError(t, err,
		`building component "fast": no module of this tt provides backend "zig"`)
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
	assert.Equal(t, []string{`components.fast.build: no module of this tt provides ` +
		`backend "zig"; its fields are not checked`}, warnings)
	assert.False(t, exists(t, filepath.Join(dir, lockFileName)))
}

// TestRun_pathDependencyOfUnknownBackend checks that the build of a path
// dependency with a component of a backend tt has not fails, naming the
// dependency, before the component is built.
func TestRun_pathDependencyOfUnknownBackend(t *testing.T) {
	t.Parallel()

	app, contrib := setupNeighbours(t)
	writeFile(t, filepath.Join(contrib, manifestFileName), strings.Replace(contribManifest,
		"backend = 'shell'\ncommand = 'sh'", "backend = 'zig'\nsteps = ['install']\n"+
			"command = 'sh'", 1))

	err := Run(context.Background(), dryOptions(app))
	require.EqualError(t, err, `path dependency "contrib": building component "native": `+
		`no module of this tt provides backend "zig"`)
	assert.False(t, exists(t, filepath.Join(app, buildDirName, pathDirName, "contrib",
		stageDirName)))
}

// errProbed stops a build at the preflight of lockProbe.
var errProbed = errors.New("probed")

// lockProbe is a build whose preflight records what the lock pins the rock
// wasm at, and stops the build.
type lockProbe struct {
	locked *[]string
}

func (p lockProbe) Preflight(_ context.Context, c *sdkbuild.Context) error {
	version, ok := c.LockedVersion("wasm")
	*p.locked = append(*p.locked, fmt.Sprintf("%s %t", version, ok))

	return errProbed
}

func (lockProbe) Run(context.Context, *sdkbuild.Context) error { return nil }

// TestRun_preflightReadsAFreshLock checks that a preflight, which runs before
// the lock is gated, reads the versions of a lock on disk that is fresh,
// and of none that is stale or missing.
func TestRun_preflightReadsAFreshLock(t *testing.T) {
	t.Parallel()

	var locked []string

	backends := buildregtest.Registry()
	require.NoError(t, backends.Add("wasm", []sdkbuild.Backend{{
		Name:     "probe",
		Produces: []sdkbuild.Result{sdkbuild.Data},
		Config:   func() any { return new(struct{}) },
		New: func(any, sdkbuild.Component, sdkbuild.Common) (sdkbuild.Build, error) {
			return lockProbe{locked: &locked}, nil
		},
	}}, nil))

	source := manifestHead + "\n[dependencies]\nwasm = '0.1.0'\n\n" +
		"[components.calc]\npath = 'calc'\n\n[components.calc.build]\nbackend = 'probe'\n"
	dir := setupProject(t, source, map[string]string{"calc/main.py": ""})

	man, _, err := manifest.ParseManifest([]byte(source))
	require.NoError(t, err)

	lock := &manifest.Lock{
		LockVersion: manifest.LockVersion, ManifestVersion: "0.1", GeneratedBy: "tt test",
		ManifestHash: man.Hash(),
		Products: map[string]manifest.LockProduct{"default": {Dependencies: []manifest.LockDependency{
			{Name: "wasm", Version: "0.1.0-1", Source: "registry"},
		}}},
	}

	data, err := lock.Marshal()
	require.NoError(t, err)
	writeFile(t, filepath.Join(dir, lockFileName), string(data))

	opts := dryOptions(dir)

	opts.Backends = backends

	// Fresh: the version the lock pins.
	require.ErrorIs(t, Run(context.Background(), opts), errProbed)

	// Stale: the manifest changed since; nothing is pinned yet.
	writeFile(t, filepath.Join(dir, manifestFileName),
		strings.Replace(source, "wasm = '0.1.0'", "wasm = '>=0.1.0'", 1))
	require.ErrorIs(t, Run(context.Background(), opts), errProbed)

	// Missing.
	require.NoError(t, os.Remove(filepath.Join(dir, lockFileName)))
	require.ErrorIs(t, Run(context.Background(), opts), errProbed)

	assert.Equal(t, []string{"0.1.0-1 true", " false", " false"}, locked)
	assert.NoFileExists(t, filepath.Join(dir, lockFileName))
}

// TestRun_preflightOfAModuleBackend checks that the build of a component of
// a backend a module provides asks its build what it needs before anything
// is resolved or built, as it asks one of the core.
func TestRun_preflightOfAModuleBackend(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, zigManifest, map[string]string{"fast/build.zig": ""})

	type zigConfig struct {
		Steps []string `toml:"steps"`
	}

	backends := buildregtest.Registry()
	require.NoError(t, backends.Add("zig", []sdkbuild.Backend{{
		Name:     "zig",
		Produces: []sdkbuild.Result{sdkbuild.Module},
		Config:   func() any { return new(zigConfig) },
		New: func(any, sdkbuild.Component, sdkbuild.Common) (sdkbuild.Build, error) {
			return zigBuild{}, nil
		},
		Incremental: false,
		OutputFrom:  sdkbuild.OutputNone,
		Exclude:     nil,
		Hook:        false,
	}}, nil))

	opts := dryOptions(dir)

	opts.Backends = backends

	err := Run(context.Background(), opts)
	require.ErrorIs(t, err, sdkbuild.ErrNoTool)
	require.EqualError(t, err, `building component "fast": build tool not found: `+
		`no-such-zig-of-tt`)
	assert.Equal(t, sdk.ExitSystem, exitcode.Code(err))
	assert.False(t, exists(t, filepath.Join(dir, lockFileName)))
}
