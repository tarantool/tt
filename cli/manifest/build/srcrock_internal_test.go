package build

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/go-luarocks/remote"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// fakeFetcher answers Search with results and Download with the path of
// rock.
type fakeFetcher struct {
	results []client.SearchResult
	rock    string
}

func (f fakeFetcher) Search(
	context.Context, string, client.SearchOpts,
) ([]client.SearchResult, error) {
	return f.results, nil
}

func (f fakeFetcher) Download(context.Context, string, client.DownloadOpts) (string, error) {
	return f.rock, nil
}

var srcDep = manifest.LockDependency{Name: "mylib", Version: "1.2.0-1", Source: "registry"}

// TestNeedsSrcRock: a source rock is built only when the servers have no rock
// this host installs of the very version.
func TestNeedsSrcRock(t *testing.T) {
	t.Parallel()

	rows := func(arches ...string) []client.SearchResult {
		out := make([]client.SearchResult, 0, len(arches))
		for _, arch := range arches {
			out = append(out, client.SearchResult{Name: "mylib", Version: "1.2.0-1", Arch: arch})
		}

		return out
	}

	// A row of another server than the rock is installed from does not count.
	elsewhere := rows(allArch)

	elsewhere[0].Server = "/elsewhere"

	for name, test := range map[string]struct {
		results []client.SearchResult
		want    bool
	}{
		"host rock":        {rows(remote.HostArch(), srcArch), false},
		"all rock":         {rows(allArch, srcArch), false},
		"source rock only": {rows("plan9-mips", srcArch), true},
		"nothing":          {nil, false},
		"another version": {[]client.SearchResult{
			{Name: "mylib", Version: "1.1.0-1", Arch: allArch},
			{Name: "mylib", Version: "1.2.0-1", Arch: srcArch, Server: "/srv"},
		}, true},
		"another server": {append(elsewhere, rows(srcArch)...), true},
	} {
		for i := range test.results {
			if test.results[i].Server == "" {
				test.results[i].Server = "/srv"
			}
		}

		got, err := needsSrcRock(t.Context(), fakeFetcher{results: test.results, rock: ""}, srcDep,
			[]string{"file:///srv/"})
		require.NoError(t, err, name)
		assert.Equal(t, test.want, got, name)
	}
}

// writeZip writes a zip archive of entries, name → content; a name ending in
// "/" is a directory and "x:" before a name marks it executable.
func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()

	file, err := os.Create(path)
	require.NoError(t, err)

	archive := zip.NewWriter(file)

	for name, content := range entries {
		mode := os.FileMode(0o644)
		if len(name) > 2 && name[:2] == "x:" {
			name, mode = name[2:], 0o755
		}

		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(mode)

		writer, err := archive.CreateHeader(header)
		require.NoError(t, err)

		_, err = writer.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, archive.Close())
	require.NoError(t, file.Close())
}

// TestFetchSrcRock: the source rock is unpacked into a directory of its own,
// cleared first, and the directory of the package in it is found.
func TestFetchSrcRock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rock := filepath.Join(dir, "mylib-1.2.0-1.src.rock")
	writeZip(t, rock, map[string]string{
		"mylib-1.2.0-1.rockspec":  "package = 'mylib'",
		"mylib/app.manifest.toml": "manifest_version = '0.1'",
		"mylib/VERSION":           "1.2.0\n",
		"x:mylib/tools/gen.sh":    "#!/bin/sh\n",
	})

	stale := filepath.Join(dir, "mylib-1.2.0-1", "stale")
	require.NoError(t, os.MkdirAll(filepath.Dir(stale), 0o755))
	require.NoError(t, os.WriteFile(stale, nil, 0o644))

	project, err := fetchSrcRock(t.Context(), fakeFetcher{results: nil, rock: rock}, srcDep, dir,
		nil)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dir, "mylib-1.2.0-1", "mylib"), project)
	assert.FileExists(t, filepath.Join(project, "VERSION"))
	assert.NoFileExists(t, stale)

	info, err := os.Stat(filepath.Join(project, "tools", "gen.sh"))
	require.NoError(t, err)
	assert.Equal(t, srcRockExecMode, info.Mode().Perm())
}

// TestFetchSrcRockRefuses: an entry that escapes the directory, and a rock of
// no tt package, fail.
func TestFetchSrcRockRefuses(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	escaping := filepath.Join(dir, "escaping.src.rock")
	writeZip(t, escaping, map[string]string{"../../evil": "x"})

	_, err := fetchSrcRock(t.Context(), fakeFetcher{results: nil, rock: escaping}, srcDep, dir,
		nil)
	require.ErrorIs(t, err, errSrcRockEntry)
	assert.NoFileExists(t, filepath.Join(filepath.Dir(dir), "evil"))

	plain := filepath.Join(dir, "plain.src.rock")
	writeZip(t, plain, map[string]string{"mylib/mylib.rockspec": "x"})

	_, err = fetchSrcRock(t.Context(), fakeFetcher{results: nil, rock: plain}, srcDep, dir, nil)
	require.ErrorIs(t, err, errSrcRockPackage)
}
