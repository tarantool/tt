package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkbuild "github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/sdk/v3"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
	"github.com/tarantool/tt/v3/cli/manifest/projectlock"
)

// genFile is the file the builds of gen generate beside the sources of
// their component, which only the Clean of gen removes.
const genFile = "gen.lua"

// errCleanFailed is what the Clean of gen fails with when told to.
var errCleanFailed = errors.New("the clean failed")

// genConfig is the configuration of the gen backend.
type genConfig struct {
	// Module is the module the build makes.
	Module string `toml:"module"`
	// FailClean has the Clean of the build fail.
	FailClean bool `toml:"fail_clean"`
}

// genCleans records the Contexts the Clean of gen ran in, and whether the
// directory of the intermediate files was still there then.
type genCleans struct {
	mu       sync.Mutex
	contexts map[string]sdkbuild.Context
	work     map[string]bool
}

// record records the Context of a Clean, and whether TT_BUILD_DIR was there.
func (r *genCleans) record(facts *sdkbuild.Context, work bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.contexts[facts.Component.Name] = *facts
	r.work[facts.Component.Name] = work
}

// genBuild is a checked block of gen: it makes a module and generates
// genFile in the directory of its component and a file in TT_BUILD_DIR.
type genBuild struct {
	config genConfig
	cleans *genCleans
}

// Run makes the files of the block.
func (b genBuild) Run(_ context.Context, facts *sdkbuild.Context) error {
	err := os.WriteFile(filepath.Join(facts.Dirs.Component, genFile), []byte("return {}\n"),
		0o600)
	if err == nil {
		err = os.WriteFile(filepath.Join(facts.Dirs.Work, "object.o"), []byte("o"), 0o600)
	}

	if err == nil {
		err = os.WriteFile(filepath.Join(facts.Dirs.Modules, b.config.Module), []byte("so"),
			0o600)
	}

	return err
}

// Clean removes genFile, what the build made outside the directories of the
// core.
func (b genBuild) Clean(_ context.Context, facts *sdkbuild.Context) error {
	_, err := os.Stat(facts.Dirs.Work)

	b.cleans.record(facts, err == nil)

	if b.config.FailClean {
		return errCleanFailed
	}

	err = os.Remove(filepath.Join(facts.Dirs.Component, genFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return err
}

var _ sdkbuild.Cleaner = genBuild{}

// genBackend is the gen backend, whose Clean records into cleans.
func genBackend(cleans *genCleans) sdkbuild.Backend {
	return sdkbuild.Backend{
		Name:     "gen",
		Produces: []sdkbuild.Result{sdkbuild.Module},
		Config:   func() any { return new(genConfig) },
		New: func(cfg any, _ sdkbuild.Component, _ sdkbuild.Common) (sdkbuild.Build, error) {
			config, ok := cfg.(*genConfig)
			if !ok {
				return nil, sdkbuild.FieldErrorf("", "not a configuration of gen")
			}

			return genBuild{config: *config, cleans: cleans}, nil
		},
		Incremental:    true,
		OutputFrom:     sdkbuild.OutputNone,
		Exclude:        []string{"/" + genFile},
		NoSourceLayout: false,
		Hook:           false,
	}
}

// genManifest is my-app with two components of gen, one incremental, and a
// clean hook that leaves a mark in the project.
const genManifest = manifestHead + `
[components.fast]
path = 'fast'

[components.fast.build]
backend = 'gen'
module = 'fast.so'
incremental = true

[components.slow]
path = 'slow'

[components.slow.build]
backend = 'gen'
module = 'slow.so'

[hooks.clean]
command = 'sh'
args = ['-c', 'echo cleaned > hook.mark']

[products.default]
components = ['lua', 'fast', 'slow']
default = true
`

// genProject builds a project of genManifest and returns its directory,
// the options of its builds and what the Clean of gen records.
func genProject(t *testing.T, manifest string) (string, Options, *genCleans) {
	t.Helper()

	dir := setupProject(t, manifest, map[string]string{
		"init.lua":      "return {}\n",
		"fast/init.lua": "return {}\n",
		"slow/init.lua": "return {}\n",
		"bad/init.lua":  "return {}\n",
	})

	cleans := &genCleans{
		mu: sync.Mutex{}, contexts: map[string]sdkbuild.Context{}, work: map[string]bool{},
	}

	backends := buildregtest.Registry()
	require.NoError(t, backends.Add("gen", []sdkbuild.Backend{genBackend(cleans)}, nil))

	opts := dryOptions(dir)

	opts.Backends = backends

	require.NoError(t, Run(context.Background(), opts))

	return dir, opts, cleans
}

// TestClean_wholeProject cleans a project that was built: the Clean of
// every component of gen ran first, with TT_BUILD_DIR still there, then the
// hook, then _build/ went, but the lock, and the rock of the package left
// .rocks/; the sources, the manifest and the lock stay, and the project
// builds again.
func TestClean_wholeProject(t *testing.T) {
	t.Parallel()

	dir, opts, cleans := genProject(t, genManifest)
	tree := filepath.Join(dir, rocksDirName)
	module := filepath.Join(tree, "lib", "tarantool", "my-app", "fast.so")

	require.FileExists(t, module)
	require.FileExists(t, filepath.Join(dir, "fast", genFile))

	result, err := Clean(context.Background(), opts, CleanOptions{Components: nil, DryRun: false})
	require.NoError(t, err)

	assert.Equal(t, []string{"fast", "slow"}, result.Cleaned)
	assert.True(t, result.Hook)
	assert.True(t, cleans.work["fast"], "the Clean runs before the core removes TT_BUILD_DIR")
	assert.Equal(t, filepath.Join(dir, "fast"), cleans.contexts["fast"].Dirs.Component)
	assert.Equal(t, "1.2.3", cleans.contexts["fast"].Env["TT_VERSION"])

	entries, err := os.ReadDir(filepath.Join(dir, buildDirName))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, projectlock.FileName, entries[0].Name())
	assert.Contains(t, result.Removed, filepath.Join(dir, buildDirName, resultsFileName))

	require.Len(t, result.Rocks, 1)
	assert.Equal(t, "my-app", result.Rocks[0].Name)
	assert.Contains(t, result.Rocks[0].Files, module)
	assert.NoFileExists(t, module)
	assert.NoFileExists(t, filepath.Join(tree, "share", "tarantool", "my-app", "init.lua"))

	assert.NoFileExists(t, filepath.Join(dir, "fast", genFile))
	assert.FileExists(t, filepath.Join(dir, "hook.mark"))
	assert.FileExists(t, filepath.Join(dir, "fast", "init.lua"))
	assert.FileExists(t, filepath.Join(dir, manifestFileName))
	assert.FileExists(t, filepath.Join(dir, lockFileName))

	require.NoError(t, Run(context.Background(), opts))
	assert.FileExists(t, module)

	// A clean of a clean project removes what the build made again.
	result, err = Clean(context.Background(), opts, CleanOptions{Components: nil, DryRun: false})
	require.NoError(t, err)
	assert.NotEmpty(t, result.Removed)
}

// TestClean_dryRun removes nothing and runs nothing, and says what a clean
// would do.
func TestClean_dryRun(t *testing.T) {
	t.Parallel()

	dir, opts, cleans := genProject(t, genManifest)
	module := filepath.Join(dir, rocksDirName, "lib", "tarantool", "my-app", "fast.so")

	result, err := Clean(context.Background(), opts, CleanOptions{Components: nil, DryRun: true})
	require.NoError(t, err)

	assert.Equal(t, []string{"fast", "slow"}, result.Cleaned)
	assert.True(t, result.Hook)
	assert.Contains(t, result.Removed, filepath.Join(dir, buildDirName, componentDirName))
	require.Len(t, result.Rocks, 1)
	assert.Contains(t, result.Rocks[0].Files, module)

	assert.Empty(t, cleans.contexts, "a dry run runs no Clean")
	assert.NoFileExists(t, filepath.Join(dir, "hook.mark"))
	assert.FileExists(t, module)
	assert.FileExists(t, filepath.Join(dir, "fast", genFile))
	assert.DirExists(t, filepath.Join(dir, buildDirName, componentDirName, "fast"))
}

// TestClean_component cleans one component: its TT_BUILD_DIR, its records
// and its files in the stage go, and the rock of the package; the others
// keep theirs, the hook does not run, and the next build builds it rather
// than skip it.
func TestClean_component(t *testing.T) {
	t.Parallel()

	dir, opts, cleans := genProject(t, genManifest)
	state := filepath.Join(dir, buildDirName)
	stage := packageStage(dir, "default")

	require.FileExists(t, incrementalPath(state, "fast"))
	require.FileExists(t, filepath.Join(stage, "lib", "my-app", "fast.so"))
	require.DirExists(t, componentLinkDir(state, "default", "fast"))

	result, err := Clean(context.Background(), opts,
		CleanOptions{Components: []string{"fast"}, DryRun: false})
	require.NoError(t, err)

	assert.NoDirExists(t, componentLinkDir(state, "default", "fast"))
	assert.Contains(t, result.Removed, componentLinkDir(state, "default", "fast"))
	assert.DirExists(t, componentLinkDir(state, "default", "slow"))

	assert.Equal(t, []string{"fast"}, result.Cleaned)
	assert.False(t, result.Hook)
	assert.NotContains(t, cleans.contexts, "slow")
	assert.NoFileExists(t, filepath.Join(dir, "hook.mark"))

	assert.NoDirExists(t, componentBuildDir(state, "fast"))
	assert.NoFileExists(t, incrementalPath(state, "fast"))
	assert.NoFileExists(t, filepath.Join(stage, "lib", "my-app", "fast.so"))
	assert.Contains(t, result.Removed, componentBuildDir(state, "fast"))
	assert.Contains(t, result.Removed, filepath.Join(stage, "lib", "my-app", "fast.so"))

	assert.DirExists(t, componentBuildDir(state, "slow"))
	assert.FileExists(t, filepath.Join(stage, "lib", "my-app", "slow.so"))
	assert.NotContains(t, recordOf(t, dir), "fast")
	assert.Contains(t, recordOf(t, dir), "slow")

	record, ok := readStageRecord(stage)
	require.True(t, ok)
	assert.NotContains(t, record.Components, "fast")
	assert.Contains(t, record.Components, "slow")

	require.Len(t, result.Rocks, 1)
	assert.NoFileExists(t, filepath.Join(dir, rocksDirName, "lib", "tarantool", "my-app",
		"slow.so"), "the rock of the package leaves .rocks whole")

	built, err := RunResult(context.Background(), opts)
	require.NoError(t, err)
	assert.False(t, built.Built["fast"].UpToDate, "a component cleaned is built again")
	assert.FileExists(t, filepath.Join(dir, rocksDirName, "lib", "tarantool", "my-app",
		"fast.so"))
}

// TestClean_unknownComponent refuses a component the manifest does not
// declare, with exit code 1, and removes nothing.
func TestClean_unknownComponent(t *testing.T) {
	t.Parallel()

	dir, opts, _ := genProject(t, genManifest)

	_, err := Clean(context.Background(), opts,
		CleanOptions{Components: []string{"nope"}, DryRun: false})
	require.ErrorIs(t, err, manifest.ErrNoComponent)
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
	assert.DirExists(t, filepath.Join(dir, buildDirName, componentDirName))
}

// TestClean_failingClean fails the clean with exit code 2 when the Clean of
// a backend fails, before the core removes anything.
func TestClean_failingClean(t *testing.T) {
	t.Parallel()

	failing := strings.Replace(genManifest, "components = ['lua', 'fast', 'slow']",
		"components = ['lua', 'fast', 'slow', 'bad']", 1) + `
[components.bad]
path = 'bad'

[components.bad.build]
backend = 'gen'
module = 'bad.so'
fail_clean = true
`

	dir, opts, _ := genProject(t, failing)

	_, err := Clean(context.Background(), opts, CleanOptions{Components: nil, DryRun: false})
	require.ErrorIs(t, err, errCleanFailed)
	assert.Equal(t, sdk.ExitSystem, exitcode.Code(err))
	assert.FileExists(t, filepath.Join(dir, buildDirName, resultsFileName))
	assert.NoFileExists(t, filepath.Join(dir, "hook.mark"))
}

// TestClean_unknownBackend cleans a component whose backend this tt does
// not have by the core alone, with a warning.
func TestClean_unknownBackend(t *testing.T) {
	t.Parallel()

	dir, opts, _ := genProject(t, genManifest)

	var warnings []string

	opts.Backends = buildregtest.Registry()
	opts.Warn = func(msg string) { warnings = append(warnings, msg) }

	result, err := Clean(context.Background(), opts,
		CleanOptions{Components: []string{"fast"}, DryRun: false})
	require.NoError(t, err)
	assert.Empty(t, result.Cleaned)
	assert.NoDirExists(t, componentBuildDir(filepath.Join(dir, buildDirName), "fast"))
	assert.Contains(t, warnings[len(warnings)-1], buildreg.ErrNoBackend.Error())
	assert.FileExists(t, filepath.Join(dir, "fast", genFile), "nothing cleans what gen made")
}
