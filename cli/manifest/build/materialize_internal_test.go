package build

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// fakeRockClient records the Install/Build calls the materializer makes, and
// the path dependencies holding a manifest it builds.
type fakeRockClient struct {
	installs  []installCall
	builds    []string
	manifests []string
	err       error
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

func (f *fakeRockClient) buildPathManifest(_ context.Context, dep manifest.LockDependency) error {
	f.manifests = append(f.manifests, dep.Name)
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
	require.NoError(t, materialize(context.Background(), registryClient, pathClient, pathClient,
		t.TempDir(), prod, nil, rockServers{defaults: servers, overrides: nil}))

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

	require.NoError(t, materialize(context.Background(), registryClient, pathClient, pathClient,
		project, prod, nil, rockServers{}))

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

	require.NoError(t, materialize(context.Background(), fake, fake, fake, project, prod, nil,
		rockServers{}))
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

	require.NoError(t, materialize(context.Background(), fake, fake, fake, t.TempDir(), prod, dev,
		rockServers{}))

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

	require.NoError(t, materialize(context.Background(), fake, fake, fake, t.TempDir(), prod, dev,
		rockServers{}))

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
		context.Background(), fake, fake, fake, project, manifest.LockProduct{}, dev,
		rockServers{}))

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

	err := materialize(context.Background(), fake, fake, fake, t.TempDir(), prod, nil,
		rockServers{})
	require.Error(t, err)
	assert.ErrorIs(t, err, errUnknownSource)
}

func TestMaterialize_registryOverrideInstallsFromDeclaredServer(t *testing.T) {
	t.Parallel()

	fake := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "checks", Version: "3.1.0-1", Source: sourceRegistry},
		{Name: "wasm", Version: "0.1.0-1", Source: sourceRegistry},
	}}
	servers := rockServers{
		defaults:  []string{"https://rocks.example.test"},
		overrides: map[string]string{"wasm": "/project/.tarantool/registry"},
	}

	require.NoError(t, materialize(context.Background(), fake, fake, fake, t.TempDir(), prod, nil,
		servers))

	require.Len(t, fake.installs, 2)
	assert.Equal(t, []string{"https://rocks.example.test"}, fake.installs[0].opts.Servers)
	assert.Equal(t, []string{"/project/.tarantool/registry"}, fake.installs[1].opts.Servers)
}

// TestMaterialize_artifactsPinTheInstall checks that the digests a lock entry
// pins reach the installer as sha256 hex by arch, and that an entry without
// them installs with nil pins - not an empty set, which would refuse every
// file.
func TestMaterialize_artifactsPinTheInstall(t *testing.T) {
	t.Parallel()

	x86, spec := strings.Repeat("a", 64), strings.Repeat("c", 64)

	fake := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "checks", Version: "3.1.0-1", Source: sourceRegistry},
		{
			Name: "emmylua", Version: "0.30.0pre1-1", Source: sourceRegistry,
			Artifacts: map[string]string{
				"linux-x86_64": "sha256:" + x86, "rockspec": "sha256:" + spec,
			},
		},
	}}

	require.NoError(t, materialize(context.Background(), fake, fake, fake, t.TempDir(), prod, nil,
		rockServers{}))

	require.Len(t, fake.installs, 2)
	assert.Nil(t, fake.installs[0].opts.Artifacts)
	assert.Equal(t, map[string]string{"linux-x86_64": x86, "rockspec": spec},
		fake.installs[1].opts.Artifacts)
}

// TestMaterialize_pinFailuresAdvise checks that an install refused for the
// lock's pins says how to get past it, and keeps the cause for errors.Is.
func TestMaterialize_pinFailuresAdvise(t *testing.T) {
	t.Parallel()

	for _, cause := range []error{luarocks.ErrArtifactNotPinned, luarocks.ErrChecksumMismatch} {
		fake := &fakeRockClient{err: fmt.Errorf("rocks.Install: %w", cause)}
		prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{{
			Name: "emmylua", Version: "0.30.0pre1-1", Source: sourceRegistry,
			Artifacts: map[string]string{"rockspec": "sha256:" + strings.Repeat("c", 64)},
		}}}

		err := materialize(context.Background(), fake, fake, fake, t.TempDir(), prod, nil,
			rockServers{})

		require.ErrorIs(t, err, cause)
		assert.Contains(t, err.Error(), "installing emmylua 0.30.0pre1-1: ")
		assert.Contains(t, err.Error(), "run tt package resolve")
	}
}

// TestMaterialize_malformedArtifactFails checks that a digest the lock holds
// in a form tt does not read stops the install rather than installing the
// rock unverified.
func TestMaterialize_malformedArtifactFails(t *testing.T) {
	t.Parallel()

	fake := &fakeRockClient{}
	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{{
		Name: "emmylua", Version: "0.30.0pre1-1", Source: sourceRegistry,
		Artifacts: map[string]string{"linux-x86_64": "md5:00"},
	}}}

	err := materialize(context.Background(), fake, fake, fake, t.TempDir(), prod, nil,
		rockServers{})

	var invalid *manifest.ValidationError

	require.ErrorAs(t, err, &invalid)
	assert.Empty(t, fake.installs)
}

func TestDependencyServers_resolvesDeclaredRegistries(t *testing.T) {
	t.Parallel()

	man := &manifest.Manifest{
		Dependencies: map[string]manifest.Dependency{
			"checks": {Source: sourceRegistry, Version: ">=3.0.0"},
			"wasm": {
				Source: sourceRegistry, Version: "0.1.0", Registry: ".tarantool/registry",
			},
		},
		DevDependencies: map[string]manifest.Dependency{
			"luatest": {
				Source: sourceRegistry, Version: "*", Registry: "https://dev.example.test/",
			},
		},
	}

	servers, err := dependencyServers(man, "/project", []string{"https://rocks.example.test"})
	require.NoError(t, err)

	assert.Equal(t, map[string]string{
		"wasm":    filepath.Join("/project", ".tarantool", "registry"),
		"luatest": "https://dev.example.test/",
	}, servers.overrides)
	assert.Equal(t, []string{"https://rocks.example.test"}, servers.of("checks"))
}
