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

	registryClient := &fakeRockClient{}
	pathClient := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "checks", Version: "3.1.0-1", Source: sourceRegistry},
		{Name: "metrics", Version: "1.5.0-1", Source: sourceRegistry},
	}}

	servers := []string{"https://rocks.example.test"}
	require.NoError(t, materialize(context.Background(), registryClient, pathClient,
		t.TempDir(), prod, servers))

	require.Len(t, registryClient.installs, 2)
	// Order is preserved (topological), each pinned to the exact version with
	// dependency resolution off.
	assert.Equal(t, "checks", registryClient.installs[0].name)
	assert.Equal(t, "3.1.0-1", registryClient.installs[0].opts.Version)
	assert.Equal(t, servers, registryClient.installs[0].opts.Servers)
	assert.Equal(t, client.DepsNone, registryClient.installs[0].opts.Deps)
	assert.Equal(t, "metrics", registryClient.installs[1].name)
	assert.Empty(t, pathClient.builds)
}

func TestMaterialize_pathDepBuildsRockspec(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	writeFile(t, filepath.Join(project, "vendor", "mylib", "mylib-scm-1.rockspec"), "-- spec")

	registryClient := &fakeRockClient{}
	pathClient := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "mylib", Source: sourcePath, Path: "vendor/mylib"},
	}}

	require.NoError(t, materialize(context.Background(), registryClient, pathClient,
		project, prod, nil))

	require.Len(t, pathClient.builds, 1)
	assert.Equal(t, filepath.Join(project, "vendor", "mylib", "mylib-scm-1.rockspec"),
		pathClient.builds[0])
	assert.Empty(t, registryClient.installs)
}

func TestMaterialize_leafPathDepIsSkipped(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	writeFile(t, filepath.Join(project, "vendor", "leaf", "leaf.lua"), "-- leaf")

	rc := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "leaf", Source: sourcePath, Path: "vendor/leaf"},
	}}

	require.NoError(t, materialize(context.Background(), rc, rc, project, prod, nil))
	assert.Empty(t, rc.builds)
	assert.Empty(t, rc.installs)
}

func TestMaterialize_unknownSource(t *testing.T) {
	t.Parallel()

	rc := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "weird", Source: "http"},
	}}

	err := materialize(context.Background(), rc, rc, t.TempDir(), prod, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, errUnknownSource)
}
