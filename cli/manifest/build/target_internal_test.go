package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/v3"
	sdkbuild "github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
	"github.com/tarantool/tt/v3/cli/manifest/native"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// This file checks Options.Target: a build for another platform than the
// machine tt runs on, its Context and contract, the state it keeps apart, the
// tree of its own it installs the rock of the package into, the backends it
// refuses before anything is built and the checks of what it made.

// otherPlatform is a platform other than the machine of the test: its OS on
// the other architecture.
func otherPlatform() native.Platform {
	if runtime.GOARCH == "arm64" {
		return native.Platform{OS: runtime.GOOS, Arch: "amd64"}
	}

	return native.Platform{OS: runtime.GOOS, Arch: "arm64"}
}

// printPlatform is a shell command that writes the platform of its build,
// <os>-<arch>, into the file path.
func printPlatform(path string) string {
	return `printf %s-%s "$TT_PLATFORM_OS" "$TT_PLATFORM_ARCH" > ` + path
}

// targetManifest is my-app with a component built by a shell command that
// writes the platform of its build among its modules, and a post_build hook
// that writes it into the project.
var targetManifest = manifestHead + `
[components.comp]
path = 'comp'

[components.comp.build]
backend = 'shell'
command = 'sh'
args = ['-c', '''` + printPlatform(`"$TT_OUTPUT_DIR/platform.txt"`) + `''']

[hooks.post_build]
command = 'sh'
args = ['-c', '''` + printPlatform("hook.txt") + `''']
`

// readFile returns the content of the file at path.
func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(data)
}

// TestRun_target checks a build for another platform: its components and
// its hooks are told the platform, its stage is in the state of the target,
// _build/target/<os>-<arch>/, and the rock of the package is installed into
// the tree of the target there, not into .rocks/, which holds the
// dependencies alone.
func TestRun_target(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, targetManifest, map[string]string{
		"init.lua": "-- init", "comp/init.lua": "return {}",
	})

	other := otherPlatform()
	opts := dryOptions(dir)

	opts.Target = other

	result, err := RunResult(context.Background(), opts)
	require.NoError(t, err)

	state := filepath.Join(dir, buildDirName, "target", other.String())

	assert.Equal(t, other, result.Target)
	assert.Equal(t, filepath.Join(state, "rock", "default"), result.Stage)
	assert.Equal(t, filepath.Join(state, "tree"), result.Tree)

	assert.Equal(t, other.String(),
		readFile(t, filepath.Join(result.Stage, "lib", "my-app", "platform.txt")))
	assert.Equal(t, other.String(), readFile(t, filepath.Join(dir, "hook.txt")))

	installed := filepath.Join(result.Tree, "lib", "tarantool", "my-app", "platform.txt")
	assert.Equal(t, other.String(), readFile(t, installed))
	assert.Equal(t, []string{installed}, result.Built["comp"].Modules)
	assert.True(t, exists(t, filepath.Join(result.Tree, "share", "tarantool", "my-app",
		"init.lua")))

	// The machine's tree and state have nothing of the build.
	assert.False(t, exists(t, filepath.Join(dir, rocksDirName, "share", "tarantool", "my-app")))
	assert.False(t, exists(t, filepath.Join(dir, rocksDirName, "lib", "tarantool", "my-app")))
	assert.False(t, exists(t, packageStage(dir, "default")))
	assert.False(t, exists(t, filepath.Join(dir, buildDirName, resultsFileName)))
	assert.True(t, exists(t, filepath.Join(state, resultsFileName)))

	// The machine is the target of a build that names it, as of one that
	// names none.
	opts.Target = native.Host()

	result, err = RunResult(context.Background(), opts)
	require.NoError(t, err)

	assert.Equal(t, native.Host(), result.Target)
	assert.Equal(t, packageStage(dir, "default"), result.Stage)
	assert.Equal(t, filepath.Join(dir, rocksDirName), result.Tree)
	assert.Equal(t, native.Host().String(),
		readFile(t, filepath.Join(result.Tree, "lib", "tarantool", "my-app", "platform.txt")))
	assert.Equal(t, native.Host().String(), readFile(t, filepath.Join(dir, "hook.txt")))
	assert.Equal(t, other.String(), readFile(t, installed), "the target's tree stays")
}

// TestRun_targetKeepsItsOwnState checks that a build for another platform
// and one for the machine keep their states apart: an incremental component
// built for one is built again for the other, each with a TT_BUILD_DIR of its
// own, and skipped next time for either, each stage keeping what its build
// made there.
func TestRun_targetKeepsItsOwnState(t *testing.T) {
	t.Parallel()

	dir := setupProject(t,
		incrementalShell(printPlatform(`"$TT_OUTPUT_DIR/platform.txt"`)+
			` && printf %s "$TT_BUILD_DIR" > "$TT_OUTPUT_DIR/build-dir.txt"`, ""),
		map[string]string{"comp/init.lua": "return {}"})

	other := otherPlatform()
	host := dryOptions(dir)
	cross := host

	cross.Target = other

	hostStage := packageStage(dir, "default")
	crossState := targetStateDir(dir, other)
	crossStage := filepath.Join(crossState, stageDirName, "default")

	require.NoError(t, Run(context.Background(), host))
	assert.Equal(t, 1, countRuns(t, dir))

	require.NoError(t, Run(context.Background(), cross))
	assert.Equal(t, 2, countRuns(t, dir), "the build for the machine is no build for another")

	for i := range 2 {
		require.NoError(t, Run(context.Background(), host))
		require.NoError(t, Run(context.Background(), cross))
		assert.Equal(t, 2, countRuns(t, dir), "%d: both are up to date", i)
	}

	stages := map[string]native.Platform{hostStage: native.Host(), crossStage: other}

	for stage, want := range stages {
		assert.Equal(t, want.String(), readFile(t, filepath.Join(stage, "lib", "my-app",
			"platform.txt")))
	}

	assert.Equal(t, componentBuildDir(filepath.Join(dir, buildDirName), "comp"),
		readFile(t, filepath.Join(hostStage, "lib", "my-app", "build-dir.txt")))
	assert.Equal(t, componentBuildDir(crossState, "comp"),
		readFile(t, filepath.Join(crossStage, "lib", "my-app", "build-dir.txt")))

	_, found := readIncremental(filepath.Join(dir, buildDirName), "comp")
	assert.True(t, found)

	_, found = readIncremental(crossState, "comp")
	assert.True(t, found)

	// A rebuild for the platform rebuilds its own state, not the machine's.
	cross.Rebuild = true

	require.NoError(t, Run(context.Background(), cross))
	assert.Equal(t, 3, countRuns(t, dir))

	require.NoError(t, Run(context.Background(), host))
	assert.Equal(t, 3, countRuns(t, dir))
}

// TestRun_targetOfABackendThatDoesNotCross checks that a build for another
// platform fails, with exit code 1, before a hook or anything else runs,
// for a component whose backend builds for the machine alone, naming the
// component, the backend and both platforms. A build for the machine builds
// it.
func TestRun_targetOfABackendThatDoesNotCross(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, manifestHead+`
[components.native]
path = 'native'

[components.native.build]
backend = 'lib'
module = 'native'

[hooks.pre_build]
command = 'sh'
args = ['-c', 'touch pre-build.txt']
`, map[string]string{"init.lua": "-- init", "native/x.c": ""})

	other := otherPlatform()
	opts := dryOptions(dir)

	opts.Tarantool = rocks.TarantoolInfo{Prefix: headersPrefix(t), Version: "3.0.0"}
	opts.Target = other

	err := Run(context.Background(), opts)
	require.ErrorIs(t, err, sdkbuild.ErrTarget)
	require.EqualError(t, err, fmt.Sprintf(`building component "native": the build cannot `+
		`be made for the target platform: the lib backend builds for the machine tt runs on `+
		`alone, %s, and the build is for %s`, native.Host(), other))
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
	assert.False(t, exists(t, filepath.Join(dir, "pre-build.txt")))
	assert.False(t, exists(t, filepath.Join(dir, lockFileName)))
	assert.False(t, exists(t, filepath.Join(dir, buildDirName, targetDirName)))

	opts.Target = native.Platform{OS: "", Arch: ""}

	require.NoError(t, Run(context.Background(), opts))
	assert.True(t, exists(t, filepath.Join(dir, "pre-build.txt")))
}

// headersPrefix returns the prefix of a Tarantool whose headers are there,
// for a build of lib.
func headersPrefix(t *testing.T) string {
	t.Helper()

	prefix := t.TempDir()

	writeFile(t, filepath.Join(prefix, "include", "tarantool", "module.h"), "")

	return prefix
}

// targetBuild is the build of a backend that builds for another platform,
// but not for darwin, as cargo on Linux.
type targetBuild struct{}

func (targetBuild) Preflight(_ context.Context, c *sdkbuild.Context) error {
	if c.Target.Cross() && c.Target.OS == "darwin" {
		return fmt.Errorf("%w: no SDK of macOS", sdkbuild.ErrTarget)
	}

	return nil
}

func (targetBuild) Run(context.Context, *sdkbuild.Context) error { return nil }

// TestRun_targetThePreflightRefuses checks that a build for a platform the
// Preflight of a backend refuses fails before anything is built, with exit
// code 1, as one of a backend that does not build for another platform.
func TestRun_targetThePreflightRefuses(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, manifestHead+`
[components.fast]
path = 'fast'

[components.fast.build]
backend = 'target'
`, map[string]string{"init.lua": "-- init", "fast/init.lua": "return {}"})

	backends := buildregtest.Registry()
	require.NoError(t, backends.Add("target", []sdkbuild.Backend{{
		Name:     "target",
		Produces: []sdkbuild.Result{sdkbuild.Module},
		Config:   func() any { return new(struct{}) },
		New: func(any, sdkbuild.Component, sdkbuild.Common) (sdkbuild.Build, error) {
			return targetBuild{}, nil
		},
		Cross: true,
	}}, nil))

	opts := dryOptions(dir)

	opts.Backends = backends
	opts.Target = native.Platform{OS: "darwin", Arch: runtime.GOARCH}

	if runtime.GOOS == "darwin" {
		opts.Target = native.Platform{OS: "linux", Arch: runtime.GOARCH}

		require.NoError(t, Run(context.Background(), opts))

		return
	}

	err := Run(context.Background(), opts)
	require.ErrorIs(t, err, sdkbuild.ErrTarget)
	require.EqualError(t, err, `building component "fast": the build cannot be made for `+
		`the target platform: no SDK of macOS`)
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))

	opts.Target = otherPlatform()

	require.NoError(t, Run(context.Background(), opts))
}

// TestRun_targetChecksWhatWasBuilt checks that what a build for another
// platform made is checked against it: a module and a program of the
// machine fail the build with exit code 2, whatever the backend said of
// itself, as a make or shell build that compiled for the machine would.
func TestRun_targetChecksWhatWasBuilt(t *testing.T) {
	t.Parallel()

	program := hostProgram(t)
	other := otherPlatform()

	for name, block := range map[string]string{
		"module": "backend = 'both'\nmodule = 'fast'",
		"program": fmt.Sprintf("backend = 'shell'\ncommand = 'cp'\nargs = ['%s', 'fast']\n"+
			"output = [{ path = 'fast', to = 'bin' }]", program),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := setupProject(t, manifestHead+`
[components.fast]
path = 'fast'

[components.fast.build]
`+block+"\n", map[string]string{"init.lua": "-- init", "fast/init.lua": "return {}"})

			opts := dryOptions(dir)

			require.NoError(t, Run(context.Background(), opts))

			opts.Target = other

			err := Run(context.Background(), opts)
			require.Error(t, err)
			assert.Contains(t, err.Error(), `building component "fast": `)
			assert.Contains(t, err.Error(), fmt.Sprintf("of the build platform: it runs on %s, "+
				"but the build is for %s", native.Host(), other))
			assert.Equal(t, sdk.ExitSystem, exitcode.Code(err))
		})
	}
}

// TestCheckModules checks that a native module of another platform than the
// build's fails the check, naming it, and that one of the build platform, a
// file that is no native one and one that is not there pass.
func TestCheckModules(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	data, err := os.ReadFile(hostProgram(t))
	require.NoError(t, err)

	writeFile(t, filepath.Join(dir, "sub", "fast.so"), string(data))
	writeFile(t, filepath.Join(dir, "init.lua"), "return {}")

	names := []string{"sub/fast.so", "init.lua", "gone.so"}

	require.NoError(t, checkModules(dir, names, native.Host()))

	other := otherPlatform()

	err = checkModules(dir, names, other)
	require.ErrorIs(t, err, errNotModule)
	require.EqualError(t, err, fmt.Sprintf("%s: not a module of the build platform: it runs "+
		"on %s, but the build is for %s", filepath.Join(dir, "sub", "fast.so"), native.Host(),
		other))
}

// TestNewPathManifests_buildForTheMachine checks that the path dependencies
// of a build for another platform are built for the machine tt runs on: they
// are installed into .rocks/, the machine's.
func TestNewPathManifests_buildForTheMachine(t *testing.T) {
	t.Parallel()

	opts := dryOptions(t.TempDir())

	opts.Target = otherPlatform()

	paths := newPathManifests(opts, nil)

	assert.Equal(t, native.Host(), paths.opts.target())
	assert.False(t, paths.opts.cross())
}

// TestClean_componentOfATarget checks that a clean of a component removes
// what the builds for other platforms made of it too - its TT_BUILD_DIR,
// its records and its files in their stages - and the trees of the targets,
// and that a clean of the whole project removes their states.
func TestClean_componentOfATarget(t *testing.T) {
	t.Parallel()

	dir := setupProject(t,
		incrementalShell(printPlatform(`"$TT_OUTPUT_DIR/platform.txt"`), ""),
		map[string]string{"comp/init.lua": "return {}"})

	other := otherPlatform()
	opts := dryOptions(dir)

	opts.Target = other

	require.NoError(t, Run(context.Background(), opts))

	state := targetStateDir(dir, other)
	module := filepath.Join(state, stageDirName, "default", "lib", "my-app", "platform.txt")

	require.True(t, exists(t, module))

	result, err := Clean(context.Background(), dryOptions(dir), CleanOptions{
		Components: []string{"comp"}, DryRun: false,
	})
	require.NoError(t, err)

	for _, path := range []string{
		componentBuildDir(state, "comp"), incrementalPath(state, "comp"), module,
		targetTree(state),
	} {
		assert.Contains(t, result.Removed, path)
		assert.False(t, exists(t, path), path)
	}

	_, found := readResults(state, nil)["comp"]
	assert.False(t, found)

	require.NoError(t, Run(context.Background(), opts))
	assert.Equal(t, 2, countRuns(t, dir), "the clean build is built again")

	_, err = Clean(context.Background(), dryOptions(dir), CleanOptions{
		Components: nil, DryRun: false,
	})
	require.NoError(t, err)
	assert.False(t, exists(t, filepath.Join(dir, buildDirName, targetDirName)))
}
