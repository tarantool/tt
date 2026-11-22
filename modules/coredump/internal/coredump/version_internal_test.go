package coredump

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		line string
		want tarantoolVersion
		refs []string
	}{
		{
			line: "Tarantool 3.9.0-0-g8b9a0f59a95b",
			want: tarantoolVersion{
				Line: "Tarantool 3.9.0-0-g8b9a0f59a95b", Enterprise: false, Tag: "3.9.0",
				Release: "3.9.0", Commit: "8b9a0f59a95b",
			},
			refs: []string{"8b9a0f59a95b", "3.9.0"},
		},
		{
			line: "Tarantool 3.10.0-entrypoint-36-gdf2e3013e1\n",
			want: tarantoolVersion{
				Line: "Tarantool 3.10.0-entrypoint-36-gdf2e3013e1", Enterprise: false,
				Tag: "3.10.0-entrypoint", Release: "3.10.0", Commit: "df2e3013e1",
			},
			refs: []string{"df2e3013e1", "3.10.0-entrypoint"},
		},
		{
			line: "Tarantool 3.0.0-rc1-0-g1234567",
			want: tarantoolVersion{
				Line: "Tarantool 3.0.0-rc1-0-g1234567", Enterprise: false, Tag: "3.0.0-rc1",
				Release: "3.0.0", Commit: "1234567",
			},
			refs: []string{"1234567", "3.0.0-rc1"},
		},
		{
			// The commit of Enterprise is of a repository of its own.
			line: "Tarantool Enterprise 3.7.0-0-g1f1ec9fdf",
			want: tarantoolVersion{
				Line: "Tarantool Enterprise 3.7.0-0-g1f1ec9fdf", Enterprise: true, Tag: "3.7.0",
				Release: "3.7.0", Commit: "1f1ec9fdf",
			},
			refs: []string{"3.7.0"},
		},
		{
			line: "Tarantool Enterprise 2.11.2-0-g1ab2f7d75-r609",
			want: tarantoolVersion{
				Line: "Tarantool Enterprise 2.11.2-0-g1ab2f7d75-r609", Enterprise: true,
				Tag: "2.11.2", Release: "2.11.2", Commit: "1ab2f7d75",
			},
			refs: []string{"2.11.2"},
		},
		{
			line: "Tarantool 2.11.1-0-g96877bd3c-dirty",
			want: tarantoolVersion{
				Line: "Tarantool 2.11.1-0-g96877bd3c-dirty", Enterprise: false, Tag: "2.11.1",
				Release: "2.11.1", Commit: "96877bd3c",
			},
			refs: []string{"96877bd3c", "2.11.1"},
		},
		{
			// A build without git describe.
			line: "Tarantool 3.9.0",
			want: tarantoolVersion{
				Line: "Tarantool 3.9.0", Enterprise: false, Tag: "3.9.0", Release: "3.9.0",
				Commit: "",
			},
			refs: []string{"3.9.0"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.line, func(t *testing.T) {
			t.Parallel()

			got, ok := parseVersion(testCase.line)
			require.True(t, ok)
			assert.Equal(t, testCase.want, got)
			assert.Equal(t, testCase.refs, got.refs())
		})
	}
}

func TestParseVersionNotTarantool(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"", "LuaJIT 2.1.0", "Tarantool", "Tarantool x.y.z", "tarantool 3.9.0",
	} {
		got, ok := parseVersion(line)
		assert.False(t, ok, line)
		assert.Equal(t, line, got.Line)
	}
}

func TestReadVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "version")
	require.NoError(t, os.WriteFile(path, []byte("Tarantool 3.9.0-0-g8b9a0f59a95b\n"+
		"Target: Linux-x86_64-RelWithDebInfo\n"), 0o644))

	line, err := readVersion(path)
	require.NoError(t, err)
	assert.Equal(t, "Tarantool 3.9.0-0-g8b9a0f59a95b", line)

	require.NoError(t, os.WriteFile(path, nil, 0o644))

	line, err = readVersion(path)
	require.NoError(t, err)
	assert.Empty(t, line)

	_, err = readVersion(filepath.Join(t.TempDir(), "version"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
