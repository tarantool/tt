package backend

import (
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			be, err := New(tc.name, lrbuild.Flags{}, false)
			require.NoError(t, err)
			assert.IsType(t, tc.want, be)
		})
	}
}

func TestNewUnknownBackend(t *testing.T) {
	t.Parallel()

	be, err := New("cmake", lrbuild.Flags{}, false)
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

	assert.NoError(t, requireAbsPaths("/abs/cwd", "/abs/out"))
	assert.Error(t, requireAbsPaths("rel/cwd", "/abs/out"))
	assert.Error(t, requireAbsPaths("/abs/cwd", "rel/out"))
}
