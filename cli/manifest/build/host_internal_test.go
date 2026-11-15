package build

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luarocks/client"

	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/execfile"
	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/internal/cmakepolicy"
)

// lastEnvValue returns the value of the last KEY=VALUE entry for key, as
// exec.Cmd resolves duplicate keys; "" if absent.
func lastEnvValue(environ []string, key string) string {
	value, _ := sdkbuild.LookupEnv(environ, key)

	return value
}

// testContract is the contract of the build of the component comp of the
// package pkg.
func testContract() contract {
	return contract{
		outputDir:   "/out",
		component:   "comp",
		buildDir:    "/proj/_build/component/comp",
		projectRoot: "/proj",
		pkg:         "pkg",
		version:     "1.2.3",
		target:      sdkbuild.Target{OS: "linux", Arch: "amd64"},
		tarantool:   sdkbuild.Tarantool{Prefix: "", IncludeDir: "", Version: "", Executable: ""},
		hook:        false,
	}
}

func TestEnvironContract(t *testing.T) {
	t.Parallel()

	env := environment{
		block:       map[string]string{"FOO": "bar", "BAZ": "qux"},
		projectRoot: t.TempDir(),
		contract:    testContract().environ(),
	}

	got := env.environ()

	assert.Equal(t, "/out", lastEnvValue(got, "TT_OUTPUT_DIR"))
	assert.Equal(t, "/proj", lastEnvValue(got, "TT_PROJECT_ROOT"))
	assert.Equal(t, "pkg", lastEnvValue(got, "TT_PACKAGE"))
	assert.Equal(t, "comp", lastEnvValue(got, "TT_COMPONENT_NAME"))
	assert.Equal(t, "1.2.3", lastEnvValue(got, "TT_VERSION"))
	assert.Equal(t, "linux", lastEnvValue(got, "TT_PLATFORM_OS"))
	assert.Equal(t, "amd64", lastEnvValue(got, "TT_PLATFORM_ARCH"))
	assert.Equal(t, "/proj/_build/component/comp", lastEnvValue(got, "TT_BUILD_DIR"))

	// The env of the block is merged in, after the environment of tt, in key
	// order, and the contract comes last. The environment of tt is not counted
	// here: a parallel test may make a rocks client, which sets the CMake policy
	// version in it, but not in the environment of a build.
	assert.Equal(t, "bar", lastEnvValue(got, "FOO"))
	assert.Equal(t, "qux", lastEnvValue(got, "BAZ"))

	block := slices.Index(got, "BAZ=qux")
	require.GreaterOrEqual(t, block, 0)
	assert.Equal(t, []string{"BAZ=qux", "FOO=bar"}, got[block:block+2])
	assert.Equal(t, testContract().environ(), got[len(got)-len(testContract().environ()):])

	// Context.Env has the variables of the build: the block's and the
	// contract's.
	vars := env.variables()
	assert.Equal(t, "bar", vars["FOO"])
	assert.Equal(t, "/out", vars["TT_OUTPUT_DIR"])
	assert.NotContains(t, vars, "HOME")
}

// TestEnvironContractVarWinsOverBlock: a TT_* variable the env of a block
// names is overridden by the contract's, which comes after it.
func TestEnvironContractVarWinsOverBlock(t *testing.T) {
	t.Parallel()

	env := environment{
		block: map[string]string{
			"TT_OUTPUT_DIR": "/fake", "TT_BUILD_DIR": "/fake", "TT_TARANTOOL_PREFIX": "/fake",
		},
		projectRoot: "",
		contract: func() []string {
			c := testContract()

			c.tarantool.Prefix = "/opt/tarantool"

			return c.environ()
		}(),
	}

	got := env.environ()

	assert.Equal(t, "/out", lastEnvValue(got, "TT_OUTPUT_DIR"))
	assert.Equal(t, "/proj/_build/component/comp", lastEnvValue(got, "TT_BUILD_DIR"))
	assert.Equal(t, "/opt/tarantool", lastEnvValue(got, "TT_TARANTOOL_PREFIX"))
	assert.Less(t, slices.Index(got, "TT_OUTPUT_DIR=/fake"),
		slices.Index(got, "TT_OUTPUT_DIR=/out"))

	vars := env.variables()
	assert.Equal(t, "/out", vars["TT_OUTPUT_DIR"])
	assert.Equal(t, "/opt/tarantool", vars["TT_TARANTOOL_PREFIX"])
}

// TestHookContract: a hook, which runs around the builds of all the
// components, has no output directory, component and directory of
// intermediate files, and is told the rest as a build is.
func TestHookContract(t *testing.T) {
	t.Parallel()

	hook := testContract()

	hook.hook = true
	hook.tarantool = sdkbuild.Tarantool{
		Prefix: "/opt/tarantool", IncludeDir: "/opt/tarantool/include/tarantool", Version: "",
		Executable: "",
	}

	got := hook.environ()

	for _, key := range []string{"TT_OUTPUT_DIR", "TT_COMPONENT_NAME", "TT_BUILD_DIR"} {
		_, ok := sdkbuild.LookupEnv(got, key)
		assert.False(t, ok, key)
	}

	assert.Equal(t, []string{
		"TT_PROJECT_ROOT=/proj", "TT_PACKAGE=pkg", "TT_VERSION=1.2.3", "TT_PLATFORM_OS=linux",
		"TT_PLATFORM_ARCH=amd64", "TT_TARANTOOL_PREFIX=/opt/tarantool",
		"TT_TARANTOOL_INCLUDE_DIR=/opt/tarantool/include/tarantool",
	}, got)
}

// TestContractTarantoolUnknown: the variables of Tarantool are set only when
// tt knows it.
func TestContractTarantoolUnknown(t *testing.T) {
	t.Parallel()

	for _, hook := range []bool{false, true} {
		c := testContract()

		c.hook = hook

		for _, key := range []string{"TT_TARANTOOL_PREFIX", "TT_TARANTOOL_INCLUDE_DIR"} {
			_, ok := sdkbuild.LookupEnv(c.environ(), key)
			assert.False(t, ok, "%s is set for an unknown Tarantool", key)
		}
	}
}

// TestEnvironLeavesOutCMakePolicyOfRocks: the CMake policy version tt sets
// for the rocks it builds does not reach the build of a component, the user's
// code, while one the user sets, in the environment or in the env of the
// block, does.
func TestEnvironLeavesOutCMakePolicyOfRocks(t *testing.T) {
	t.Setenv(cmakepolicy.Variable, "")
	require.NoError(t, os.Unsetenv(cmakepolicy.Variable))

	env := environment{block: nil, projectRoot: "", contract: testContract().environ()}

	_, err := rocks.New(rocks.BuildConfig(rocks.TarantoolInfo{}, rocks.ConfigOptions{
		Tree: t.TempDir(), WorkingDir: t.TempDir(), Servers: []string{}, Logger: nil,
	})).Client(client.BackendNative)
	require.NoError(t, err)
	require.Equal(t, cmakepolicy.Version, os.Getenv(cmakepolicy.Variable),
		"a rocks client builds with the policy version")

	_, ok := sdkbuild.LookupEnv(env.environ(), cmakepolicy.Variable)
	assert.False(t, ok, "a component builds with the policy version of tt")

	env.block = map[string]string{cmakepolicy.Variable: "3.10"}
	assert.Equal(t, "3.10", lastEnvValue(env.environ(), cmakepolicy.Variable))

	env.block = nil

	t.Setenv(cmakepolicy.Variable, "3.10")
	assert.Equal(t, "3.10", lastEnvValue(env.environ(), cmakepolicy.Variable))
}

// writeProjectProgram puts an executable shell script named name into the
// project's .tarantool/bin and returns the project root.
func writeProjectProgram(t *testing.T, name, script string) string {
	t.Helper()

	project := t.TempDir()
	dir := ProjectBinDir(project)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	// The tests run the script while parallel tests fork: execfile keeps the
	// forks off the descriptor it writes it through.
	require.NoError(t, execfile.WriteFile(filepath.Join(dir, name), []byte(script), 0o755))

	return project
}

func TestEnvironPutsProjectBinFirst(t *testing.T) {
	t.Parallel()

	project := writeProjectProgram(t, "tool", "#!/bin/sh\n")

	env := environment{
		block: map[string]string{"PATH": "/opt/extra/bin"}, projectRoot: project, contract: nil,
	}

	assert.Equal(t, ProjectBinDir(project)+string(os.PathListSeparator)+"/opt/extra/bin",
		lastEnvValue(env.environ(), "PATH"))
}

func TestEnvironWithoutProjectBinKeepsPath(t *testing.T) {
	t.Parallel()

	env := environment{
		block: map[string]string{"PATH": "/opt/extra/bin"}, projectRoot: t.TempDir(),
		contract: nil,
	}

	assert.Equal(t, "/opt/extra/bin", lastEnvValue(env.environ(), "PATH"))
}

func TestLookPathInSkipsRelativeEntries(t *testing.T) {
	t.Parallel()

	project := writeProjectProgram(t, "tool", "#!/bin/sh\n")

	wd, err := os.Getwd()
	require.NoError(t, err)

	relative, err := filepath.Rel(wd, ProjectBinDir(project))
	require.NoError(t, err)

	_, err = lookPathIn("tool", []string{"PATH=" + relative})
	require.ErrorIs(t, err, exec.ErrNotFound)

	path, err := lookPathIn("tool", []string{"PATH=" + strings.Join(
		[]string{relative, ProjectBinDir(project)}, string(os.PathListSeparator))})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(ProjectBinDir(project), "tool"), path)

	path, err = lookPathIn("./tool", []string{"PATH=" + ProjectBinDir(project)})
	require.NoError(t, err)
	assert.Equal(t, "./tool", path)
}

// TestHostLooksOnlyInThePathOfTheBuild: a PATH the env of a build block sets
// is where its programs are looked for, and the only place: a program of the
// PATH of tt is not found by LookPath, and a command of it fails to start
// with exec.ErrNotFound, rather than run the program tt would.
func TestHostLooksOnlyInThePathOfTheBuild(t *testing.T) {
	t.Parallel()

	// sh is in the PATH of tt, and in no directory of the PATH of the build.
	_, err := exec.LookPath("sh")
	require.NoError(t, err)

	cwd := t.TempDir()
	host := testHost("", cwd, map[string]string{"PATH": t.TempDir()})

	_, err = host.LookPath("sh")
	require.ErrorIs(t, err, exec.ErrNotFound)

	cmd := host.Command(context.Background(), "sh", "-c", "true")
	assert.Equal(t, []string{"sh", "-c", "true"}, cmd.Args)

	err = host.Run(cmd)
	require.ErrorIs(t, err, exec.ErrNotFound)

	_, err = host.Output(host.Command(context.Background(), "sh", "-c", "true"))
	require.ErrorIs(t, err, exec.ErrNotFound)

	// An empty PATH has nothing either.
	host = testHost("", cwd, map[string]string{"PATH": ""})

	_, err = host.LookPath("sh")
	require.ErrorIs(t, err, exec.ErrNotFound)

	// A path is taken as it is, relative to the working directory of the
	// build.
	require.NoError(t, execfile.WriteFile(filepath.Join(cwd, "tool"), []byte("#!/bin/sh\n"), 0o755))

	path, err := host.LookPath("./tool")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cwd, "tool"), path)
}

// testHost is the host of a build of the project at project, in cwd.
func testHost(project, cwd string, block map[string]string) host {
	return host{
		env: environment{
			block: block, projectRoot: project, contract: testContract().environ(),
		},
		cwd:        cwd,
		showOutput: false,
		pinned:     map[string]string{"tarantool": "3.4.0"},
	}
}

// TestHostRunsProgramFromProjectBin: a command of a build finds a program of
// .tarantool/bin of the project by its name, and runs in the working
// directory of the build with its environment.
func TestHostRunsProgramFromProjectBin(t *testing.T) {
	t.Parallel()

	project := writeProjectProgram(t, "tt-test-project-tool",
		"#!/bin/sh\nprintf '%s|%s|%s' \"$0 $*\" \"$(pwd)\" \"$TT_COMPONENT_NAME\"\n")
	cwd := t.TempDir()
	host := testHost(project, cwd, nil)

	path, err := host.LookPath("tt-test-project-tool")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(ProjectBinDir(project), "tt-test-project-tool"), path)

	_, err = host.LookPath("no-such-tool-of-tt")
	require.Error(t, err)

	cmd := host.Command(context.Background(), "tt-test-project-tool", "build")
	assert.Equal(t, cwd, cmd.Dir)
	assert.Equal(t, []string{"tt-test-project-tool", "build"}, cmd.Args)

	out, err := host.Output(cmd)
	require.NoError(t, err)

	wd, err := filepath.EvalSymlinks(cwd)
	require.NoError(t, err)
	assert.Equal(t, path+" build|"+wd+"|comp", string(out))

	version, ok := host.LockedVersion("tarantool")
	assert.True(t, ok)
	assert.Equal(t, "3.4.0", version)

	_, ok = host.LockedVersion("luatest")
	assert.False(t, ok)
}

// TestHostFailure: a command that fails is an error that names it, from Run
// and from Output alike.
func TestHostFailure(t *testing.T) {
	t.Parallel()

	host := testHost(t.TempDir(), t.TempDir(), nil)

	err := host.Run(host.Command(context.Background(), "sh", "-c", "exit 3"))
	require.ErrorContains(t, err, "failed to run")
	assert.Contains(t, err.Error(), "exit status 3")

	_, err = host.Output(host.Command(context.Background(), "sh", "-c", "exit 4"))
	require.ErrorContains(t, err, "failed to run")
	assert.Contains(t, err.Error(), "exit status 4")
}

// TestRunLogsTheCommand pins that a build logs the command it runs, as it
// is given, and the directory it runs in, at the debug level tt -V shows:
// with -V the output of the command is streamed, and the command line comes
// before it, not only when the command fails. The test swaps the process
// logger, so it does not run in parallel.
func TestRunLogsTheCommand(t *testing.T) {
	logger, recorder := logtest.New(t)
	previous := slog.Default()

	t.Cleanup(func() { slog.SetDefault(previous) })
	slog.SetDefault(logger)

	dir := t.TempDir()
	host := testHost(t.TempDir(), dir, nil)

	require.NoError(t, host.Run(host.Command(context.Background(), "sh", "-c", "exit 0")))

	var found []logtest.Record

	for _, record := range recorder.Records() {
		if strings.HasPrefix(record.Message, "running ") {
			found = append(found, record)
		}
	}

	require.Len(t, found, 1)
	assert.Equal(t, slog.LevelDebug, found[0].Level)
	assert.Equal(t, "running sh -c exit 0", found[0].Message)
	assert.Equal(t, dir, found[0].Map()["dir"])
}

// TestPinnedVersions: the versions a build reads from the lock are those of
// its closure.
func TestPinnedVersions(t *testing.T) {
	t.Parallel()

	pinned := pinnedVersions(manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "luatest", Version: "1.0.1-1"}, {Name: "tarawasm-sdk", Version: "0.3.0-1"},
	}})

	assert.Equal(t, map[string]string{"luatest": "1.0.1-1", "tarawasm-sdk": "0.3.0-1"}, pinned)
}
