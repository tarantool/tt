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

// TestParseRelease checks that a release is read by its numbers, compared
// by them, 3.10.0 after 3.9.0, and that what is not x.y.z is not read.
func TestParseRelease(t *testing.T) {
	t.Parallel()

	got, ok := parseRelease("3.10.2")
	require.True(t, ok)
	assert.Equal(t, release{3, 10, 2}, got)

	older, ok := parseRelease("3.9.0")
	require.True(t, ok)
	assert.Positive(t, got.compare(older))
	assert.Negative(t, older.compare(got))
	assert.Zero(t, got.compare(got))
	assert.Equal(t, release{3, 10, 0}, got.minor())

	for _, text := range []string{
		"", "3.9", "3.9.0.1", "3.9.0-rc1", "v3.9.0", " 3.9.0", "3.9.x",
		"3.9.99999999999999999999",
	} {
		_, ok := parseRelease(text)
		assert.False(t, ok, text)
	}
}

// mustMinors returns checked by the minors of its ends.
func mustMinors(t *testing.T, checked versionRange) minorRange {
	t.Helper()

	minors, ok := checked.minors()
	require.True(t, ok, checked)

	return minors
}

// TestRangeExcludes checks that a release is out of a range of versions by
// its minor alone, as the manifest of tarantool-gdb-tools means the range:
// every patch of every minor from the one of the first end to the one of
// the last, those included, is in it, 3.9.1 in 3.0.2..3.9.0 and 3.3.0 in
// 3.3.5..3.9.0. What is no release, of the range or the one checked,
// excludes nothing: a range an end of which is none has no minors.
func TestRangeExcludes(t *testing.T) {
	t.Parallel()

	checked := mustMinors(t, versionRange{Min: "3.3.5", Max: "3.9.0"})

	for version, want := range map[string]bool{
		"3.2.9": true, "3.3.0": false, "3.3.4": false, "3.3.5": false, "3.6.0": false,
		"3.9.0": false, "3.9.1": false, "3.9.12": false, "3.10.0": true, "2.11.8": true,
		"4.3.0": true, "": false, "3.9": false,
	} {
		assert.Equal(t, want, checked.excludes(version), version)
	}

	wide := mustMinors(t, versionRange{Min: "3.0.2", Max: "3.9.0"})
	assert.False(t, wide.excludes("3.9.1"))
	assert.False(t, wide.excludes("3.0.0"))
	assert.True(t, wide.excludes("2.11.8"))

	for _, bad := range []versionRange{
		{Min: "3.3.5", Max: ""}, {Min: "", Max: "3.9.0"}, {Min: "", Max: ""},
		{Min: "3.3", Max: "3.9.0"}, {Min: "3.3.5", Max: "3.9.0-rc1"},
	} {
		_, ok := bad.minors()
		assert.False(t, ok, bad)
	}
}

// TestMinorRangeString checks that a range is named by its minors, one
// alone when both ends are of it.
func TestMinorRangeString(t *testing.T) {
	t.Parallel()

	for checked, want := range map[versionRange]string{
		{Min: "3.3.5", Max: "3.9.0"}:  "3.3 to 3.9",
		{Min: "3.9.0", Max: "3.9.2"}:  "3.9",
		{Min: "3.10.1", Max: "4.0.0"}: "3.10 to 4.0",
	} {
		assert.Equal(t, want, mustMinors(t, checked).String(), checked)
	}
}
