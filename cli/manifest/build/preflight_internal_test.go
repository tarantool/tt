package build

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
)

// zigBuild is the build of the zig backend of a module, which the build does
// not run.
type zigBuild struct{}

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
	require.ErrorIs(t, err, errNoBackend)
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

// TestRun_moduleBackendFailsFirst checks that the build of a component of a
// backend a module provides fails before anything is resolved or built: the
// build runs the backends of the core only.
func TestRun_moduleBackendFailsFirst(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, zigManifest, map[string]string{"fast/build.zig": ""})

	type zigConfig struct {
		Steps []string `toml:"steps"`
	}

	backends := buildreg.Core()
	require.NoError(t, backends.Add("zig", []sdkbuild.Backend{{
		Name:   "zig",
		Kinds:  []string{sdkbuild.KindModule},
		Config: func() any { return new(zigConfig) },
		New: func(any, sdkbuild.Component, sdkbuild.Common) (sdkbuild.Build, error) {
			return zigBuild{}, nil
		},
		Incremental: false,
		OutputFrom:  sdkbuild.OutputNone,
		Exclude:     nil,
	}}, nil))

	opts := dryOptions(dir)

	opts.Backends = backends

	err := Run(context.Background(), opts)
	require.ErrorIs(t, err, errModuleBackend)
	require.EqualError(t, err, `building component "fast": backend "zig" is of module "zig": `+
		`the build runs the backends of the core only`)
	assert.False(t, exists(t, filepath.Join(dir, lockFileName)))
}
