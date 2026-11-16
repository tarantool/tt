package rocks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-luarocks/client"

	"github.com/tarantool/tt/v3/cli/tarantool"
)

// checksVersions is what a server lists of checks: a release, older ones,
// scm-1, and a rock whose name only contains it.
var checksVersions = []client.SearchResult{
	{Name: "checks", Version: "scm-1"},
	{Name: "checks", Version: "3.4.1-1"},
	{Name: "checks", Version: "3.10.0-1"},
	{Name: "checks", Version: "dev-1"},
	{Name: "checks", Version: "1.0-3"},
	{Name: "luatypechecks", Version: "9.0.0-1"},
}

func TestNewestRelease(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "3.10.0-1", newestRelease(checksVersions, "checks"))
	assert.Equal(t, "3.10.0-1", newestRelease(checksVersions, "tarantool/checks"))
	assert.Empty(t, newestRelease([]client.SearchResult{{Name: "x", Version: "scm-1"}}, "x"))
	assert.Empty(t, newestRelease(checksVersions, "nosuch"))
}

func TestPinRelease(t *testing.T) {
	t.Parallel()

	var queried [][]string

	search := func(_ context.Context, servers []string, name string) (
		[]client.SearchResult, error,
	) {
		queried = append(queried, servers)

		if name == "broken" {
			return nil, errors.New("no such host")
		}

		return checksVersions, nil
	}

	for _, test := range []struct {
		args    []string
		want    []string
		servers []string
	}{
		{
			args:    []string{"install", "checks"},
			want:    []string{"install", "checks", "3.10.0-1"},
			servers: []string{defaultServer},
		},
		{
			args:    []string{"--tree", "x", "install", "checks", "--local"},
			want:    []string{"--tree", "x", "install", "checks", "3.10.0-1", "--local"},
			servers: []string{defaultServer},
		},
		{
			args:    []string{"install", "checks", "--server", "http://a http://b"},
			want:    []string{"install", "checks", "3.10.0-1", "--server", "http://a http://b"},
			servers: []string{"http://a", "http://b", defaultServer},
		},
		{
			args:    []string{"install", "--only-server=http://c", "checks"},
			want:    []string{"install", "--only-server=http://c", "checks", "3.10.0-1"},
			servers: []string{"http://c"},
		},
	} {
		queried = nil

		pinned := pinRelease(context.Background(), search, nil, test.args)
		assert.Equal(t, test.want, pinned, test.args)
		require.Len(t, queried, 1, test.args)
		assert.Equal(t, test.servers, queried[0], test.args)
	}

	// A version given, --dev, a rock file, another command: as they are,
	// and no server asked.
	for _, args := range [][]string{
		{"install", "checks", "scm-1"},
		{"install", "--dev", "checks"},
		{"install", "c-1.0-1.all.rock"},
		{"search", "checks"},
	} {
		queried = nil

		assert.Equal(t, args, pinRelease(context.Background(), search, nil, args))
		assert.Empty(t, queried, args)
	}

	// A search that fails leaves them as they are too.
	broken := []string{"install", "broken"}
	assert.Equal(t, broken, pinRelease(context.Background(), search, nil, broken))

	queried = nil

	pinRelease(context.Background(), search, []string{"http://configured"},
		[]string{"install", "checks"})
	assert.Equal(t, [][]string{{"http://configured"}}, queried)
}

func TestIncludeDirOf(t *testing.T) {
	t.Parallel()

	withHeaders := tarantool.Prefix{Dir: "/opt/tnt", IncludeDir: "/opt/tnt/include/tarantool"}

	include, done, err := includeDirOf(withHeaders, []string{"install", "checks"})
	require.NoError(t, err)
	done()
	assert.Equal(t, "/opt/tnt/include/tarantool", include)

	without := tarantool.Prefix{
		Dir: "/opt/tnt", Tried: []string{"/opt/tnt/include/tarantool"},
		Executable: "/opt/tnt/bin/tarantool",
	}
	require.NotEmpty(t, without.MissingHeaders())

	// A command that builds nothing gets the include directory of the prefix.
	include, done, err = includeDirOf(without, []string{"search", "checks"})
	require.NoError(t, err)
	done()
	assert.Equal(t, "/opt/tnt/include/tarantool", include)

	include, done, err = includeDirOf(without, []string{"install", "checks"})
	require.NoError(t, err)

	lua, err := os.ReadFile(filepath.Join(include, "lua.h"))
	require.NoError(t, err)
	assert.Contains(t, string(lua), "#define LUA_VERSION_NUM 501\n")
	assert.Contains(t, string(lua), "#error \"tt found no headers of Tarantool")
	assert.FileExists(t, filepath.Join(include, "module.h"))

	done()
	assert.NoDirExists(t, include)
}
