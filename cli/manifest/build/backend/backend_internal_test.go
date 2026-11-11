package backend

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	lrbuild "github.com/tarantool/go-luarocks/build"
)

// lastEnvValue returns the value of the last KEY=VALUE entry for key, matching
// how exec.Cmd resolves duplicate keys (last wins). It returns "" if absent.
func lastEnvValue(environ []string, key string) string {
	prefix := key + "="

	value := ""

	for _, kv := range environ {
		if strings.HasPrefix(kv, prefix) {
			value = kv[len(prefix):]
		}
	}

	return value
}

// indexOfEnv returns the index of the first entry equal to kv, or -1.
func indexOfEnv(environ []string, kv string) int {
	for i, e := range environ {
		if e == kv {
			return i
		}
	}

	return -1
}

func TestEnvironContract(t *testing.T) {
	t.Parallel()

	env := Env{
		OutputDir:   "/out",
		ProjectRoot: "/proj",
		Package:     "pkg",
		Component:   "comp",
		Version:     "1.2.3",
		OS:          "linux",
		Arch:        "amd64",
		Extra:       map[string]string{"FOO": "bar", "BAZ": "qux"},
	}

	got := env.environ()

	// All seven contract variables are present with the Env values.
	assert.Equal(t, "/out", lastEnvValue(got, "TT_OUTPUT_DIR"))
	assert.Equal(t, "/proj", lastEnvValue(got, "TT_PROJECT_ROOT"))
	assert.Equal(t, "pkg", lastEnvValue(got, "TT_PACKAGE"))
	assert.Equal(t, "comp", lastEnvValue(got, "TT_COMPONENT_NAME"))
	assert.Equal(t, "1.2.3", lastEnvValue(got, "TT_VERSION"))
	assert.Equal(t, "linux", lastEnvValue(got, "TT_PLATFORM_OS"))
	assert.Equal(t, "amd64", lastEnvValue(got, "TT_PLATFORM_ARCH"))

	// [build].env is merged in.
	assert.Equal(t, "bar", lastEnvValue(got, "FOO"))
	assert.Equal(t, "qux", lastEnvValue(got, "BAZ"))
}

// TestEnvironBuildDir: a component build gets the directory of its
// intermediate files, over one [build].env names, and a hook, which builds no
// component, none.
func TestEnvironBuildDir(t *testing.T) {
	t.Parallel()

	env := Env{BuildDir: "/proj/_build/component/comp",
		Extra: map[string]string{"TT_BUILD_DIR": "/fake"}}

	assert.Equal(t, "/proj/_build/component/comp", lastEnvValue(env.environ(), "TT_BUILD_DIR"))
	assert.Less(t, indexOfEnv(env.environ(), "TT_BUILD_DIR=/fake"),
		indexOfEnv(env.environ(), "TT_BUILD_DIR=/proj/_build/component/comp"))
	assert.Equal(t, -1, indexOfEnv(env.hookEnviron(), "TT_BUILD_DIR=/proj/_build/component/comp"))
}

func TestEnvironContractVarWinsOverExtra(t *testing.T) {
	t.Parallel()

	env := Env{OutputDir: "/real", Extra: map[string]string{"TT_OUTPUT_DIR": "/fake"}}

	got := env.environ()

	// Both entries exist, but the contract var is emitted last so exec.Cmd's
	// last-wins dedup makes it authoritative.
	assert.Equal(t, "/real", lastEnvValue(got, "TT_OUTPUT_DIR"))
	assert.Less(t,
		indexOfEnv(got, "TT_OUTPUT_DIR=/fake"),
		indexOfEnv(got, "TT_OUTPUT_DIR=/real"),
		"the contract TT_OUTPUT_DIR must be emitted after the [build].env override")
}

func TestEnvironTarantool(t *testing.T) {
	t.Parallel()

	env := Env{
		TarantoolPrefix:     "/opt/tarantool",
		TarantoolIncludeDir: "/opt/tarantool/include/tarantool",
		Extra:               map[string]string{"TT_TARANTOOL_PREFIX": "/elsewhere"},
	}

	for name, got := range map[string][]string{"build": env.environ(), "hook": env.hookEnviron()} {
		assert.Equal(t, "/opt/tarantool", lastEnvValue(got, "TT_TARANTOOL_PREFIX"), name)
		assert.Equal(t, "/opt/tarantool/include/tarantool",
			lastEnvValue(got, "TT_TARANTOOL_INCLUDE_DIR"), name)
	}
}

// TestEnvironTarantoolUnknown cannot run in parallel: it sets the variables
// in tt's own environment, which a build inherits.
func TestEnvironTarantoolUnknown(t *testing.T) {
	t.Setenv("TT_TARANTOOL_PREFIX", "")
	require.NoError(t, os.Unsetenv("TT_TARANTOOL_PREFIX"))
	t.Setenv("TT_TARANTOOL_INCLUDE_DIR", "")
	require.NoError(t, os.Unsetenv("TT_TARANTOOL_INCLUDE_DIR"))

	for name, got := range map[string][]string{
		"build": Env{}.environ(),
		"hook":  Env{}.hookEnviron(),
	} {
		for _, key := range []string{"TT_TARANTOOL_PREFIX", "TT_TARANTOOL_INCLUDE_DIR"} {
			_, ok := lookupEnv(got, key)
			assert.False(t, ok, "%s: %s is set for an unknown Tarantool", name, key)
		}
	}
}

func TestEnvironDefaultsPlatformToHost(t *testing.T) {
	t.Parallel()

	got := Env{}.environ()

	assert.Equal(t, runtime.GOOS, lastEnvValue(got, "TT_PLATFORM_OS"))
	assert.Equal(t, runtime.GOARCH, lastEnvValue(got, "TT_PLATFORM_ARCH"))
}

func TestNewDispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want any
	}{
		{BackendShell, shellBackend{}},
		{BackendMake, makeBackend{}},
		{BackendC, ccBackend{}},
		{BackendLuaC, ccBackend{}},
		{BackendCMake, cmakeBackend{}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			be, err := New(testCase.name, lrbuild.Flags{}, false)
			require.NoError(t, err)
			assert.IsType(t, testCase.want, be)
		})
	}
}

func TestNewUnknownBackend(t *testing.T) {
	t.Parallel()

	be, err := New("scons", lrbuild.Flags{}, false)
	require.Error(t, err)
	assert.Nil(t, be)
}

func TestNewCcCarriesFlagsAndVerbosity(t *testing.T) {
	t.Parallel()

	flags := lrbuild.Flags{CC: "clang", Ext: ".so", LuaIncDir: "/inc"}

	be, err := New(BackendC, flags, true)
	require.NoError(t, err)

	cc, ok := be.(ccBackend)
	require.True(t, ok)
	assert.Equal(t, flags, cc.flags)
	assert.True(t, cc.showOutput)
}

func TestRequireAbsPaths(t *testing.T) {
	t.Parallel()

	require.NoError(t, requireAbsPaths("/abs/cwd", "/abs/out"))
	require.Error(t, requireAbsPaths("rel/cwd", "/abs/out"))
	assert.Error(t, requireAbsPaths("/abs/cwd", "rel/out"))
}
