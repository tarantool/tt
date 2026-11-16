package inventory_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
	"github.com/tarantool/tt/v3/cli/manifest/internal/treetest"
	"github.com/tarantool/tt/v3/cli/manifest/inventory"
	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// guestWithPrograms is the manifest of the guest monitoring, whose
// components make the programs mon-tool, by the output of a shell build,
// and agent, of kind = "executable".
const guestWithPrograms = `manifest_version = '0.1'

[package]
name = 'monitoring'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[products.default]
components = ['lua', 'tool', 'agent']
default = true

[components.lua]
path = '.'

[components.tool]
path = 'tool'

[components.tool.build]
backend = 'shell'
command = 'sh'
output = [{ path = 'build/mon-tool', to = 'bin' }]

[components.agent]
path = 'agent'
kind = 'executable'

[components.agent.build]
backend = 'exe'
`

// bin is the path of the program name in bin/ of the tree of fixture.
func (tr tree) bin(name string) string {
	return filepath.Join(state.BinDir(tr.lay), name)
}

// uninstallMonitoring uninstalls the guest monitoring from fixture, with the
// backends of the tests.
func uninstallMonitoring(t *testing.T, fixture tree) *inventory.UninstallResult {
	t.Helper()

	result, err := inventory.Uninstall(t.Context(), inventory.UninstallOptions{
		ProjectDir: fixture.dir, Package: "monitoring", Yes: true,
		Backends: buildregtest.Registry(),
	})
	require.NoError(t, err)

	return result
}

// TestUninstallRemovesTheProgramsOfTheGuestsRock covers a guest tt installed
// as a rock: the programs its rock_manifest lists in bin/ go with it, and no
// other file of bin/.
func TestUninstallRemovesTheProgramsOfTheGuestsRock(t *testing.T) {
	t.Parallel()

	fixture := newTree(t)

	require.NoError(t, state.WriteMetadata(fixture.lay, "monitoring",
		[]byte(guestWithPrograms), lockTOML(t, guestWithPrograms, nil), "2.0.0"))

	treetest.Install(t, fixture.lay.Tree,
		treetest.Rock{Name: "monitoring", Version: "2.0.0", Files: map[string]string{
			"lua/monitoring/init.lua": "-- monitoring\n",
			"bin/mon-tool":            "binary\n",
			"bin/agent":               "binary\n",
		}},
		treetest.Rock{Name: "luatest", Version: "1.0.1", Files: map[string]string{
			"lua/luatest/init.lua": "-- luatest\n",
			"bin/luatest":          "#!/bin/sh\n",
		}})
	fixture.write(t, fixture.bin("stranger"), "laid by hand\n")

	uninstallMonitoring(t, fixture)

	assert.NoFileExists(t, fixture.bin("mon-tool"))
	assert.NoFileExists(t, fixture.bin("agent"))
	assert.False(t, fixture.rockExists("monitoring"))
	assert.FileExists(t, fixture.bin("luatest"), "the program of another rock stays")
	assert.FileExists(t, fixture.bin("stranger"), "a file the guest did not install stays")
}

// TestUninstallRemovesTheProgramsALegacyGuestLaidOut covers a guest an older
// tt installed: its files laid out by hand, its programs in bin/ recorded
// nowhere. They are told by its manifest, among the files of bin/ no rock
// owns.
func TestUninstallRemovesTheProgramsALegacyGuestLaidOut(t *testing.T) {
	t.Parallel()

	fixture := newTree(t)

	require.NoError(t, state.WriteMetadata(fixture.lay, "monitoring",
		[]byte(guestWithPrograms), lockTOML(t, guestWithPrograms, nil), "2.0.0"))

	treetest.Install(t, fixture.lay.Tree, treetest.Rock{
		Name: "luatest", Version: "1.0.1", Files: map[string]string{
			"lua/luatest/init.lua": "-- luatest\n",
			"bin/luatest":          "#!/bin/sh\n",
		},
	})

	fixture.write(t, filepath.Join(fixture.lay.Share, "monitoring", "init.lua"), "-- old\n")
	fixture.write(t, fixture.bin("mon-tool"), "binary\n")
	fixture.write(t, fixture.bin("agent"), "binary\n")
	fixture.write(t, fixture.bin("stranger"), "laid by hand\n")

	uninstallMonitoring(t, fixture)

	assert.NoFileExists(t, fixture.bin("mon-tool"))
	assert.NoFileExists(t, fixture.bin("agent"))
	assert.False(t, fixture.rockExists("monitoring"))
	assert.False(t, fixture.metadataExists("monitoring"))
	assert.FileExists(t, fixture.bin("luatest"))
	assert.FileExists(t, fixture.bin("stranger"), "a file no component of the guest makes stays")
}

// TestUninstallRemovesTheProgramsOfARockTheManifestLost covers a dependency
// an older tt extracted from an archive over the tree, whose manifest then
// did not list it: the tree is repaired first, as a build repairs it, so the
// dependency goes by its rock_manifest, its program in bin/ and a module out
// of its own directory too.
func TestUninstallRemovesTheProgramsOfARockTheManifestLost(t *testing.T) {
	t.Parallel()

	fixture := newTree(t)

	fixture.installGuest(t, pkg{
		name: "monitoring", version: "2.0.0", pins: map[string]string{"metrics": "1.0.0"},
	})
	treetest.Remove(t, fixture.lay.Tree, "metrics", "1.0.0")
	treetest.Install(t, fixture.lay.Tree, treetest.Rock{
		Name: "metrics", Version: "1.0.0", Files: map[string]string{
			"lua/metrics/init.lua": "-- metrics\n",
			"lua/metrics.lua":      "return require('metrics.init')\n",
			"bin/metrics-cli":      "#!/bin/sh\n",
		},
	})

	// The tree manifest an older tt left: it lists no rock at all.
	require.NoError(t, os.Remove(filepath.Join(fixture.lay.Share, "rocks", "manifest")))

	result := uninstallMonitoring(t, fixture)

	assert.Equal(t, []string{"metrics"}, result.RemovedDependencies)
	assert.False(t, fixture.rockExists("metrics"))
	assert.NoFileExists(t, filepath.Join(fixture.lay.Share, "metrics.lua"))
	assert.NoFileExists(t, fixture.bin("metrics-cli"))
}
