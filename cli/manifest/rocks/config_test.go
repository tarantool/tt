package rocks_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/build"
	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/go-luarocks/rockspec"
	"github.com/tarantool/tt/cli/manifest/rocks"
)

// sampleTarantool is the Tarantool info the config tests build a config from.
func sampleTarantool() rocks.TarantoolInfo {
	return rocks.TarantoolInfo{
		Executable: "/opt/tt/bin/tarantool",
		Prefix:     "/opt/tt",
		Version:    "3.1.0",
	}
}

func TestDefaultServers(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{rocks.ServerTarantool, rocks.ServerLuaRocks}, rocks.DefaultServers())
}

func TestBuildConfigDefaults(t *testing.T) {
	t.Parallel()

	cfg := rocks.BuildConfig(sampleTarantool(), rocks.ConfigOptions{
		Tree:       "/app/.rocks",
		WorkingDir: "/app",
		Servers:    nil,
		Logger:     nil,
	})

	assert.Equal(t, "/app/.rocks", cfg.Tree)
	assert.Equal(t, "/app", cfg.WorkingDir)
	assert.Equal(t, rocks.DefaultServers(), cfg.Servers)
	// rocks.tarantool.org is in the default list, so it must be marked insecure.
	assert.Equal(t, []string{"rocks.tarantool.org"}, cfg.InsecureServers)

	assert.Equal(t, "/opt/tt/bin/tarantool", cfg.Tarantool.Executable)
	assert.Equal(t, "/opt/tt", cfg.Tarantool.Prefix)
	assert.Equal(t, "3.1.0", cfg.Tarantool.Version)
	assert.Equal(t, "/opt/tt/include/tarantool", cfg.Tarantool.IncludeDir)
}

func TestBuildConfigCustomServersNoInsecure(t *testing.T) {
	t.Parallel()

	servers := []string{"http://127.0.0.1:8080/", "https://luarocks.org/"}
	cfg := rocks.BuildConfig(sampleTarantool(), rocks.ConfigOptions{
		Tree:       "/app/.rocks",
		WorkingDir: "/app",
		Servers:    servers,
		Logger:     nil,
	})

	assert.Equal(t, servers, cfg.Servers)
	// No rocks.tarantool.org among the servers, so none are marked insecure.
	assert.Empty(t, cfg.InsecureServers)
}

func TestClientBackends(t *testing.T) {
	t.Parallel()

	adapter := rocks.New(rocks.BuildConfig(sampleTarantool(), rocks.ConfigOptions{
		Tree:       "/app/.rocks",
		WorkingDir: "/app",
		Servers:    nil,
		Logger:     nil,
	}))

	for _, backend := range []client.Backend{client.BackendNative, client.BackendLua} {
		rocksClient, err := adapter.Client(backend)
		require.NoError(t, err)
		assert.NotNil(t, rocksClient)
	}
}

func TestFlagsMatchDeriveFlags(t *testing.T) {
	t.Parallel()

	cfg := rocks.BuildConfig(sampleTarantool(), rocks.ConfigOptions{
		Tree:       "/app/.rocks",
		WorkingDir: "/app",
		Servers:    nil,
		Logger:     nil,
	})
	adapter := rocks.New(cfg)

	flags := adapter.Flags()

	// One source of flags: the adapter must return exactly what the library
	// derives for the same config.
	assert.Equal(t, build.DeriveFlags(cfg), flags)

	// Host-independent invariants.
	assert.Contains(t, flags.CFLAGS, "-fPIC")
	assert.Contains(t, flags.CFLAGS, "-I/opt/tt/include/tarantool")
	assert.Equal(t, ".so", flags.Ext)
	assert.NotEmpty(t, flags.LIBFLAG)

	// Per-OS shared-link flag.
	switch runtime.GOOS {
	case "darwin":
		assert.Equal(t,
			[]string{"-bundle", "-undefined", "dynamic_lookup", "-all_load"}, flags.LIBFLAG)
	default:
		assert.Equal(t, []string{"-shared"}, flags.LIBFLAG)
	}
}

func TestChecksum(t *testing.T) {
	t.Parallel()

	const md5 = "d41d8cd98f00b204e9800998ecf8427e"

	withMD5 := evalRockspec(t, `package = "x"
version = "1.0-1"
source = { url = "https://example.com/x-1.0.tar.gz", md5 = "`+md5+`" }
`)

	got, ok := rocks.Checksum(withMD5)
	assert.True(t, ok)
	assert.Equal(t, "md5:"+md5, got)

	withoutMD5 := evalRockspec(t, `package = "x"
version = "1.0-1"
source = { url = "https://example.com/x-1.0.tar.gz" }
`)

	got, ok = rocks.Checksum(withoutMD5)
	assert.False(t, ok)
	assert.Empty(t, got)

	// A nil spec carries no checksum.
	got, ok = rocks.Checksum(nil)
	assert.False(t, ok)
	assert.Empty(t, got)
}

// evalRockspec writes body to a temp .rockspec and evaluates it into a typed
// rockspec, failing the test on error.
func evalRockspec(t *testing.T, body string) *luarocks.Rockspec {
	t.Helper()

	path := filepath.Join(t.TempDir(), "x-1.0-1.rockspec")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	spec, err := rockspec.Eval(path, luarocks.RockspecConfig{})
	require.NoError(t, err)

	return spec
}
