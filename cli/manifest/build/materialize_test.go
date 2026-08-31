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
		t.TempDir(), prod, nil, servers))

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
		project, prod, nil, nil))

	require.Len(t, pathClient.builds, 1)
	assert.Equal(t, filepath.Join(project, "vendor", "mylib", "mylib-scm-1.rockspec"),
		pathClient.builds[0])
	assert.Empty(t, registryClient.installs)
}

func TestMaterialize_leafPathDepIsSkipped(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	writeFile(t, filepath.Join(project, "vendor", "leaf", "leaf.lua"), "-- leaf")

	fake := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "leaf", Source: sourcePath, Path: "vendor/leaf"},
	}}

	require.NoError(t, materialize(context.Background(), fake, fake, project, prod, nil, nil))
	assert.Empty(t, fake.builds)
	assert.Empty(t, fake.installs)
}

// TestMaterialize_devDepsInstalledAfterProduct pins that the dev closure goes
// into the same tree as the product's, after it, so a rock in both is already
// present at the product's version when the dev list reaches it.
func TestMaterialize_devDepsInstalledAfterProduct(t *testing.T) {
	t.Parallel()

	fake := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "metrics", Version: "1.5.0-1", Source: sourceRegistry},
	}}
	dev := []manifest.LockDependency{
		{Name: "checks", Version: "3.1.0-1", Source: sourceRegistry},
		{Name: "luatest", Version: "1.0.1-1", Source: sourceRegistry},
	}

	require.NoError(t, materialize(context.Background(), fake, fake, t.TempDir(), prod, dev, nil))

	require.Len(t, fake.installs, 3)
	assert.Equal(t, "metrics", fake.installs[0].name)
	assert.Equal(t, "checks", fake.installs[1].name)
	assert.Equal(t, "luatest", fake.installs[2].name)
	assert.Equal(t, "1.0.1-1", fake.installs[2].opts.Version)
	assert.Equal(t, client.DepsNone, fake.installs[2].opts.Deps)
}

// TestMaterialize_devDepSharedWithProductInstalledOnce covers the overlap the
// resolver deliberately allows: a rock pinned in both closures is one rock in
// one tree, so it is installed once rather than reinstalled at the same
// version.
func TestMaterialize_devDepSharedWithProductInstalledOnce(t *testing.T) {
	t.Parallel()

	fake := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "checks", Version: "3.1.0-1", Source: sourceRegistry},
	}}
	dev := []manifest.LockDependency{
		{Name: "checks", Version: "3.1.0-1", Source: sourceRegistry},
		{Name: "luatest", Version: "1.0.1-1", Source: sourceRegistry},
	}

	require.NoError(t, materialize(context.Background(), fake, fake, t.TempDir(), prod, dev, nil))

	require.Len(t, fake.installs, 2)
	assert.Equal(t, "checks", fake.installs[0].name)
	assert.Equal(t, "luatest", fake.installs[1].name)
}

// TestMaterialize_devPathDepBuilt pins that a path-sourced dev dependency goes
// through the same build path as a runtime one.
func TestMaterialize_devPathDepBuilt(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	writeFile(t, filepath.Join(project, "dev", "helper", "helper-scm-1.rockspec"), "-- spec")

	fake := &fakeRockClient{}
	dev := []manifest.LockDependency{
		{Name: "helper", Source: sourcePath, Path: "dev/helper"},
	}

	require.NoError(t, materialize(
		context.Background(), fake, fake, project, manifest.LockProduct{}, dev, nil))

	require.Len(t, fake.builds, 1)
	assert.Equal(t,
		filepath.Join(project, "dev", "helper", "helper-scm-1.rockspec"), fake.builds[0])
}

func TestMaterialize_unknownSource(t *testing.T) {
	t.Parallel()

	fake := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "weird", Source: "http"},
	}}

	err := materialize(context.Background(), fake, fake, t.TempDir(), prod, nil, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, errUnknownSource)
}
