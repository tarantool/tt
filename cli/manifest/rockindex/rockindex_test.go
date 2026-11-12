package rockindex_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luarocks/remote"

	"github.com/tarantool/tt/v3/cli/manifest/rockindex"
)

// writeFiles writes files, name to content, into dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(dir, 0o755))

	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
}

// sha256Hex is the sha256 of data in hex.
func sha256Hex(data string) string {
	sum := sha256.Sum256([]byte(data))

	return hex.EncodeToString(sum[:])
}

// readIndex reads the index at path.
func readIndex(t *testing.T, path string) *remote.RocksIndex {
	t.Helper()

	f, err := os.Open(path)
	require.NoError(t, err)

	defer func() { _ = f.Close() }()

	idx, err := remote.ReadRocksIndex(f)
	require.NoError(t, err)

	return idx
}

// versions lists the versions idx has of name, in order.
func versions(idx *remote.RocksIndex, name string) []string {
	out := make([]string, 0, len(idx.Repository[name]))
	for version := range idx.Repository[name] {
		out = append(out, version)
	}

	sort.Strings(out)

	return out
}

// TestWriteListsTheFilesOfADirectory checks that every rock and rockspec of
// the directory is listed at a URL relative to the index, with its sha256 and
// size, the other files left out, and that the index keeps the rules a reader
// reads it by and names the files it lists, digests included.
func TestWriteListsTheFilesOfADirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"stat-0.3.2-1.rockspec":          "spec of stat",
		"stat-0.3.2-1.linux-x86_64.rock": "linux rock",
		"stat-0.3.2-1.src.rock":          "source rock",
		"kv-api-1.0.0-2.all.rock":        "kv rock",
		"README":                         "not a rock",
		"manifest":                       "return {}",
		".stat-0.3.3-1.all.rock":         "a hidden file",
		"stat-0.3.2-1.rockspec.orig":     "a copy",
	})

	result, err := rockindex.Write(context.Background(), rockindex.Options{Dir: dir})
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dir, rockindex.File), result.Index)
	assert.Equal(t, 2, result.Listed)
	assert.Zero(t, result.Kept)
	assert.Empty(t, result.Dropped)

	idx := readIndex(t, result.Index)
	assert.Equal(t, []string{"0.3.2-1"}, versions(idx, "stat"))
	assert.Equal(t, []string{"1.0.0-2"}, versions(idx, "kv-api"))

	stat := idx.Repository["stat"]["0.3.2-1"]
	assert.Equal(t, &remote.IndexFile{
		SHA256: sha256Hex("spec of stat"), Size: int64(len("spec of stat")),
		URL: "stat-0.3.2-1.rockspec",
	}, stat.Rockspec)
	assert.Equal(t, []remote.IndexArtifact{
		{
			Arch: "linux-x86_64", SHA256: sha256Hex("linux rock"), Size: int64(len("linux rock")),
			URL: "stat-0.3.2-1.linux-x86_64.rock",
		},
		{
			Arch: "src", SHA256: sha256Hex("source rock"), Size: int64(len("source rock")),
			URL: "stat-0.3.2-1.src.rock",
		},
	}, stat.Artifacts)
	assert.False(t, stat.Yanked)

	kv := idx.Repository["kv-api"]["1.0.0-2"]
	assert.Nil(t, kv.Rockspec)
	assert.Len(t, kv.Artifacts, 1)

	require.NoError(t, idx.Validate(context.Background(), remote.ValidateOptions{
		InsecureServers: nil, Fetch: true, Base: dir, UserAgent: "",
	}))

	// The same directory is the same bytes.
	first, err := os.ReadFile(result.Index)
	require.NoError(t, err)

	_, err = rockindex.Write(context.Background(), rockindex.Options{Dir: dir})
	require.NoError(t, err)

	second, err := os.ReadFile(result.Index)
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second))

	info, err := os.Stat(result.Index)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

// TestWriteServesWhatItLists checks the index through the reader tt resolves
// with: the newest version, the URL of its rock and the digest of every file,
// which the lock pins.
func TestWriteServesWhatItLists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"stat-0.3.1-1.all.rock":  "rock 0.3.1",
		"stat-0.3.1-1.rockspec":  "spec 0.3.1",
		"stat-0.3.2-1.all.rock":  "rock 0.3.2",
		"stat-0.3.2-1.rockspec":  "spec 0.3.2",
		"other-1.0.0-1.all.rock": "rock other",
	})

	_, err := rockindex.Write(context.Background(), rockindex.Options{Dir: dir})
	require.NoError(t, err)

	found, err := remote.NewIndex(dir, remote.IndexOptions{}).
		Query(context.Background(), "stat", "")
	require.NoError(t, err)
	require.Len(t, found, 2)

	byVersion := map[string][]string{}

	for _, rock := range found {
		for _, artifact := range rock.Artifacts {
			byVersion[rock.Version.Raw] = append(byVersion[rock.Version.Raw],
				artifact.Arch+"="+artifact.SHA256)
		}

		assert.Equal(t, filepath.Join(dir, "stat-"+rock.Version.Raw+".rockspec"), rock.SpecURL)
	}

	assert.Equal(t, map[string][]string{
		"0.3.1-1": {"all=" + sha256Hex("rock 0.3.1"), "rockspec=" + sha256Hex("spec 0.3.1")},
		"0.3.2-1": {"all=" + sha256Hex("rock 0.3.2"), "rockspec=" + sha256Hex("spec 0.3.2")},
	}, byVersion)
}

// TestWriteUpdatesAnIndex checks the merge with an index that is there: a
// version of the directory is listed anew and stays yanked, one whose files
// are gone is dropped, and one listed with files elsewhere is kept.
func TestWriteUpdatesAnIndex(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"stat-0.3.1-1.all.rock": "rock 0.3.1",
		"stat-0.3.2-1.all.rock": "rock 0.3.2",
	})

	_, err := rockindex.Write(context.Background(), rockindex.Options{Dir: dir})
	require.NoError(t, err)

	path := filepath.Join(dir, rockindex.File)
	idx := readIndex(t, path)

	yanked := idx.Repository["stat"]["0.3.1-1"]

	yanked.Yanked = true
	idx.Repository["stat"]["0.3.1-1"] = yanked

	elsewhere := remote.IndexVersion{
		Artifacts: []remote.IndexArtifact{{
			Arch: "all", SHA256: sha256Hex("rock 0.2.0"), Size: 0,
			URL: "https://example.test/releases/0.2.0/stat-0.2.0-1.all.rock",
		}},
		Rockspec: nil,
		Yanked:   false,
	}

	idx.Repository["stat"]["0.2.0-1"] = elsewhere

	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, idx.Write(file))
	require.NoError(t, file.Close())

	require.NoError(t, os.Remove(filepath.Join(dir, "stat-0.3.2-1.all.rock")))
	writeFiles(t, dir, map[string]string{"stat-0.3.1-1.linux-x86_64.rock": "linux rock 0.3.1"})

	result, err := rockindex.Write(context.Background(), rockindex.Options{Dir: dir})
	require.NoError(t, err)

	assert.Equal(t, 1, result.Listed)
	assert.Equal(t, 1, result.Kept)
	assert.Equal(t, []string{"stat 0.3.2-1"}, result.Dropped)

	idx = readIndex(t, path)
	assert.Equal(t, []string{"0.2.0-1", "0.3.1-1"}, versions(idx, "stat"))
	assert.Equal(t, elsewhere, idx.Repository["stat"]["0.2.0-1"])

	listed := idx.Repository["stat"]["0.3.1-1"]
	assert.True(t, listed.Yanked)
	assert.Len(t, listed.Artifacts, 2)
}

// TestWriteRefusesAVersionListedElsewhere checks that a version the index
// lists with files elsewhere is not listed again from the directory, and the
// index is left as it was.
func TestWriteRefusesAVersionListedElsewhere(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	index := filepath.Join(root, rockindex.File)
	writeFiles(t, filepath.Join(root, "v1"),
		map[string]string{"stat-1.0.0-1.all.rock": "rock"})
	writeFiles(t, filepath.Join(root, "v1-again"),
		map[string]string{"stat-1.0.0-1.all.rock": "rebuilt"})

	_, err := rockindex.Write(context.Background(), rockindex.Options{
		Dir: filepath.Join(root, "v1"), Index: index,
	})
	require.NoError(t, err)

	before, err := os.ReadFile(index)
	require.NoError(t, err)

	_, err = rockindex.Write(context.Background(), rockindex.Options{
		Dir: filepath.Join(root, "v1-again"), Index: index,
	})
	require.ErrorIs(t, err, rockindex.ErrListedElsewhere)
	assert.Contains(t, err.Error(), "stat 1.0.0-1")
	assert.Contains(t, err.Error(), "listed with files elsewhere, at v1/stat-1.0.0-1.all.rock")
	assert.Contains(t, err.Error(), "new revision")

	after, err := os.ReadFile(index)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

// TestWriteAnIndexApart checks an index kept apart from the rocks: below its
// directory the files are named by the path to them, elsewhere only a base
// URL names them, and the base URL is an https one.
func TestWriteAnIndexApart(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rocks := filepath.Join(root, "site", "v1.0.0")
	writeFiles(t, rocks, map[string]string{
		"stat-1.0.0-1.all.rock": "rock",
		"stat-1.0.0-1.rockspec": "spec",
	})

	index := filepath.Join(root, "site", rockindex.File)
	_, err := rockindex.Write(context.Background(), rockindex.Options{Dir: rocks, Index: index})
	require.NoError(t, err)

	entry := readIndex(t, index).Repository["stat"]["1.0.0-1"]
	assert.Equal(t, "v1.0.0/stat-1.0.0-1.rockspec", entry.Rockspec.URL)
	assert.Equal(t, "v1.0.0/stat-1.0.0-1.all.rock", entry.Artifacts[0].URL)
	assert.NoFileExists(t, filepath.Join(rocks, rockindex.File))

	found, err := remote.NewIndex(filepath.Join(root, "site"), remote.IndexOptions{}).
		Query(context.Background(), "stat", "")
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, filepath.Join(rocks, "stat-1.0.0-1.all.rock"), found[0].URL)

	apart := filepath.Join(root, "index", rockindex.File)

	_, err = rockindex.Write(context.Background(), rockindex.Options{Dir: rocks, Index: apart})
	require.ErrorIs(t, err, rockindex.ErrNotBelowIndex)
	assert.Contains(t, err.Error(), "--base-url")

	base := "https://github.com/example/stat/releases/download/v1.0.0/"

	_, err = rockindex.Write(context.Background(), rockindex.Options{
		Dir: rocks, Index: apart, BaseURL: base,
	})
	require.NoError(t, err)

	entry = readIndex(t, apart).Repository["stat"]["1.0.0-1"]
	assert.Equal(t, base+"stat-1.0.0-1.all.rock", entry.Artifacts[0].URL)

	// The same tag again lists the version anew rather than refusing it.
	_, err = rockindex.Write(context.Background(), rockindex.Options{
		Dir: rocks, Index: apart, BaseURL: base,
	})
	require.NoError(t, err)

	_, err = rockindex.Write(context.Background(), rockindex.Options{
		Dir: rocks, Index: filepath.Join(root, "plain", rockindex.File),
		BaseURL: "http://example.test/v1.0.0",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plain http")
}

// TestWriteKeepsAnIndexItCannotRead checks that an index that does not read
// fails the write and is not replaced.
func TestWriteKeepsAnIndexItCannotRead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"stat-1.0.0-1.all.rock": "rock",
		rockindex.File:          `{"format": 99, "repository": {}}`,
	})

	_, err := rockindex.Write(context.Background(), rockindex.Options{Dir: dir})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not read")

	data, err := os.ReadFile(filepath.Join(dir, rockindex.File))
	require.NoError(t, err)
	assert.JSONEq(t, `{"format": 99, "repository": {}}`, string(data))
}

// TestWriteRefusesAFileItCannotList checks that a rock whose name breaks a
// rule of the index fails the write, rather than being left out of the
// catalogue tt reads first.
func TestWriteRefusesAFileItCannotList(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"stat-1.0.0-1.installed.rock": "rock"})

	_, err := rockindex.Write(context.Background(), rockindex.Options{Dir: dir})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stat 1.0.0-1")
	assert.NoFileExists(t, filepath.Join(dir, rockindex.File))
}

// TestMakeServer checks that a directory is made a server of LuaRocks
// manifests and of the index, and that IfIndexed keeps an index current
// without writing one where there is none.
func TestMakeServer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"stat-1.0.0-1.all.rock": "rock"})

	result, err := rockindex.MakeServer(context.Background(), dir, rockindex.ServerOptions{
		HTML: false, IfIndexed: true, Warn: nil,
	})
	require.NoError(t, err)
	assert.Zero(t, result.Listed)
	assert.FileExists(t, filepath.Join(dir, "manifest"))
	assert.NoFileExists(t, filepath.Join(dir, rockindex.File))

	result, err = rockindex.MakeServer(context.Background(), dir, rockindex.ServerOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Listed)
	assert.FileExists(t, filepath.Join(dir, rockindex.File))

	writeFiles(t, dir, map[string]string{"stat-1.1.0-1.all.rock": "rock 1.1.0"})

	result, err = rockindex.MakeServer(context.Background(), dir, rockindex.ServerOptions{
		HTML: false, IfIndexed: true, Warn: nil,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Listed)
	assert.Equal(t, []string{"1.0.0-1", "1.1.0-1"},
		versions(readIndex(t, filepath.Join(dir, rockindex.File)), "stat"))
}
