package build

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luarocks/client"

	"github.com/tarantool/tt/cli/manifest"
)

// fakeRockClient records the Install/Build calls the materializer makes.
type fakeRockClient struct {
	installs []installCall
	builds   []string
	err      error
}

type installCall struct {
	name string
	opts client.InstallOpts
}

func (f *fakeRockClient) Install(_ context.Context, name string, opts client.InstallOpts) error {
	f.installs = append(f.installs, installCall{name: name, opts: opts})
	return f.err
}

func (f *fakeRockClient) Build(_ context.Context, specPath string, _ client.BuildOpts) error {
	f.builds = append(f.builds, specPath)
	return f.err
}

func TestMaterialize_registryDepsPinnedNoDeps(t *testing.T) {
	t.Parallel()

	rc := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "checks", Version: "3.1.0-1", Source: sourceRegistry},
		{Name: "metrics", Version: "1.5.0-1", Source: sourceRegistry},
	}}

	require.NoError(t, materialize(context.Background(), rc, t.TempDir(), prod))

	require.Len(t, rc.installs, 2)
	// Order is preserved (topological), each pinned to the exact version with
	// dependency resolution off.
	assert.Equal(t, "checks", rc.installs[0].name)
	assert.Equal(t, "3.1.0-1", rc.installs[0].opts.Version)
	assert.Equal(t, client.DepsNone, rc.installs[0].opts.Deps)
	assert.Equal(t, "metrics", rc.installs[1].name)
	assert.Empty(t, rc.builds)
}

func TestMaterialize_pathDepBuildsRockspec(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	writeFile(t, filepath.Join(project, "vendor", "mylib", "mylib-scm-1.rockspec"), "-- spec")

	rc := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "mylib", Source: sourcePath, Path: "vendor/mylib"},
	}}

	require.NoError(t, materialize(context.Background(), rc, project, prod))

	require.Len(t, rc.builds, 1)
	assert.Equal(t, filepath.Join(project, "vendor", "mylib", "mylib-scm-1.rockspec"), rc.builds[0])
	assert.Empty(t, rc.installs)
}

func TestMaterialize_leafPathDepIsSkipped(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	writeFile(t, filepath.Join(project, "vendor", "leaf", "leaf.lua"), "-- leaf")

	rc := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "leaf", Source: sourcePath, Path: "vendor/leaf"},
	}}

	require.NoError(t, materialize(context.Background(), rc, project, prod))
	assert.Empty(t, rc.builds)
	assert.Empty(t, rc.installs)
}

func TestMaterialize_unknownSource(t *testing.T) {
	t.Parallel()

	rc := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "weird", Source: "http"},
	}}

	err := materialize(context.Background(), rc, t.TempDir(), prod)
	require.Error(t, err)
	assert.ErrorIs(t, err, errUnknownSource)
}
