package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRun_stageDropsWhatIsStale pins that a file deleted from the sources,
// and the files of a component no longer in the product, leave the stage and
// .rocks/ with the next build, the rest staying.
func TestRun_stageDropsWhatIsStale(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, baseManifest, map[string]string{
		"init.lua":     "-- init",
		"lib/foo.lua":  "-- foo",
		"native/x.txt": "keep native dir",
	})
	stage := packageStage(dir, "default")
	tree := filepath.Join(dir, rocksDirName)

	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, []string{
		"lib/my-app/fast_hash.so", "lua/my-app/init.lua", "lua/my-app/lib/foo.lua",
		"lua/my-app/version.lua",
	}, relSet(t, stage))

	require.NoError(t, os.Remove(filepath.Join(dir, "lib", "foo.lua")))
	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	assert.Equal(t, []string{
		"lib/my-app/fast_hash.so", "lua/my-app/init.lua", "lua/my-app/version.lua",
	}, relSet(t, stage))
	assert.NoDirExists(t, filepath.Join(stage, "lua", "my-app", "lib"))
	assert.NoFileExists(t, filepath.Join(tree, "share/tarantool/my-app/lib/foo.lua"))
	assert.FileExists(t, filepath.Join(tree, "share/tarantool/my-app/init.lua"))
	assert.FileExists(t, filepath.Join(tree, "lib/tarantool/my-app/fast_hash.so"))

	// The native component leaves the product, and its module the stage.
	writeFile(t, filepath.Join(dir, manifestFileName), strings.Replace(baseManifest,
		"components = ['lua', 'native']", "components = ['lua']", 1))
	require.NoError(t, Run(context.Background(), dryOptions(dir)))

	assert.Equal(t, []string{"lua/my-app/init.lua", "lua/my-app/version.lua"},
		relSet(t, stage))
	assert.NoFileExists(t, filepath.Join(tree, "lib/tarantool/my-app/fast_hash.so"))
}

// skippedManifest is my-app with a Lua component and an incremental one,
// whose build writes native.so into TT_OUTPUT_DIR only when it is not there,
// as a make target that is up to date does.
const skippedManifest = `manifest_version = '0.1'

[package]
name = 'my-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'

[components.lua]
path = 'lua'

[components.comp]
path = 'comp'

[components.comp.build]
backend = 'shell'
command = 'sh'
args = ['-c', '''echo ran >> ../runs.log && mkdir -p "$TT_OUTPUT_DIR" &&
[ -f "$TT_OUTPUT_DIR/native.so" ] || cp lib.txt "$TT_OUTPUT_DIR/native.so"''']
incremental = true
`

// TestRun_stageKeepsWhatASkippedBuildWrote pins that what an incremental
// component wrote into the stage stays there, and in .rocks/, while its build
// is skipped, a stale file of another component going all the same.
func TestRun_stageKeepsWhatASkippedBuildWrote(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, skippedManifest, map[string]string{
		"lua/init.lua": "return {}",
		"lua/old.lua":  "return {}",
		"comp/lib.txt": "lib",
	})
	stage := packageStage(dir, "default")
	native := filepath.Join(dir, rocksDirName, "lib", "tarantool", "my-app", "native.so")

	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, 1, countRuns(t, dir))

	require.NoError(t, os.Remove(filepath.Join(dir, "lua", "old.lua")))
	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, 1, countRuns(t, dir), "comp did not change: its build is skipped")

	assert.Equal(t, []string{
		"lib/my-app/native.so", "lua/my-app/init.lua", "lua/my-app/version.lua",
	}, relSet(t, stage))
	assert.FileExists(t, native)
	assert.NoFileExists(t, filepath.Join(dir, rocksDirName, "share/tarantool/my-app/old.lua"))

	// A build of comp that writes nothing - its target is up to date - keeps
	// native.so its own.
	writeFile(t, filepath.Join(dir, "comp", "README"), "a change of the inputs")
	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, 2, countRuns(t, dir))
	assert.FileExists(t, filepath.Join(stage, "lib", "my-app", "native.so"))
	assert.FileExists(t, native)
}

// TestRun_stageOfAComponentBuild pins that a build of one component
// (--component) keeps in the stage what the others put there, and drops what
// is stale of its own; and that one with no record of the stage, which does
// not know the files of the others, removes nothing.
func TestRun_stageOfAComponentBuild(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, baseManifest, map[string]string{
		"init.lua":     "-- init",
		"lib/foo.lua":  "-- foo",
		"stale.lua":    "-- stale",
		"native/x.txt": "x",
	})
	stage := packageStage(dir, "default")
	native := filepath.Join(stage, "lib", "my-app", "fast_hash.so")

	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	require.FileExists(t, native)

	lua := dryOptions(dir)

	lua.Component = "lua"

	require.NoError(t, os.Remove(filepath.Join(dir, "lib", "foo.lua")))
	require.NoError(t, Run(context.Background(), lua))

	assert.FileExists(t, native, "native was not built: its module stays")
	assert.NoFileExists(t, filepath.Join(stage, "lua", "my-app", "lib", "foo.lua"))
	assert.FileExists(t, filepath.Join(dir, rocksDirName, "lib/tarantool/my-app/fast_hash.so"))

	require.NoError(t, os.Remove(stageRecordPath(stage)))
	require.NoError(t, os.Remove(filepath.Join(dir, "stale.lua")))
	require.NoError(t, Run(context.Background(), lua))

	assert.FileExists(t, native)
	assert.FileExists(t, filepath.Join(stage, "lua", "my-app", "stale.lua"),
		"no record: nothing is removed")
	assert.NoFileExists(t, stageRecordPath(stage), "nor recorded")

	// A build of every component starts the stage anew.
	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.Equal(t, []string{
		"lib/my-app/fast_hash.so", "lua/my-app/init.lua", "lua/my-app/version.lua",
	}, relSet(t, stage))
	assert.FileExists(t, stageRecordPath(stage))
}

// TestRun_stageRebuildDropsWhatAnEarlierBuildWrote pins that a module a
// backend no longer writes stays its own while it is there, as a make target
// that is up to date is, and that --rebuild, which starts from an empty
// stage, drops it.
func TestRun_stageRebuildDropsWhatAnEarlierBuildWrote(t *testing.T) {
	t.Parallel()

	write := func(name string) string {
		return strings.Replace(baseManifest,
			"args = ['-c', 'echo built > fast_hash.so']\noutput = ['fast_hash.so']",
			`args = ['-c', 'mkdir -p "$TT_OUTPUT_DIR" && echo built > "$TT_OUTPUT_DIR/`+
				name+`"']`, 1)
	}

	dir := setupProject(t, write("old.so"), map[string]string{
		"init.lua": "-- init", "native/x.txt": "x",
	})
	staged := filepath.Join(packageStage(dir, "default"), "lib", "my-app")

	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	require.FileExists(t, filepath.Join(staged, "old.so"))

	writeFile(t, filepath.Join(dir, manifestFileName), write("new.so"))
	require.NoError(t, Run(context.Background(), dryOptions(dir)))
	assert.FileExists(t, filepath.Join(staged, "old.so"))
	assert.FileExists(t, filepath.Join(staged, "new.so"))

	rebuild := dryOptions(dir)

	rebuild.Rebuild = true

	require.NoError(t, Run(context.Background(), rebuild))
	assert.NoFileExists(t, filepath.Join(staged, "old.so"))
	assert.FileExists(t, filepath.Join(staged, "new.so"))
	assert.NoFileExists(t, filepath.Join(dir, rocksDirName, "lib/tarantool/my-app/old.so"))
}

// TestRun_stageOfAPathDependency pins that a Lua file deleted from a path
// dependency holding a manifest leaves its stage in the consumer's _build/ and
// the consumer's .rocks/.
func TestRun_stageOfAPathDependency(t *testing.T) {
	t.Parallel()

	app, contrib := setupNeighbours(t)
	stage := filepath.Join(pathState(app, "contrib"), stageDirName, "default")
	share := filepath.Join(app, rocksDirName, "share", "tarantool", "contrib")

	require.NoError(t, Run(context.Background(), dryOptions(app)))
	require.FileExists(t, filepath.Join(stage, "lua", "contrib", "util.lua"))
	require.FileExists(t, filepath.Join(share, "util.lua"))

	require.NoError(t, os.Remove(filepath.Join(contrib, "util.lua")))
	require.NoError(t, Run(context.Background(), dryOptions(app)))

	assert.NoFileExists(t, filepath.Join(stage, "lua", "contrib", "util.lua"))
	assert.NoFileExists(t, filepath.Join(share, "util.lua"))
	assert.FileExists(t, filepath.Join(share, "init.lua"))
	assert.FileExists(t, filepath.Join(stage, "lib", "contrib", "where.so"))
}
