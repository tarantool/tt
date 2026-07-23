package install

import (
	"archive/tar"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadHeaderWithDeps reads the metadata of a with-deps archive and reports
// it carries a runtime.
func TestReadHeaderWithDeps(t *testing.T) {
	t.Parallel()

	path := archiveSpec{
		name: "my-app", version: "1.0.0", withRuntime: "3.0.5",
		lockDeps: []LockDep{{Name: "luasocket", Version: "3.0.4", Source: "registry"}},
	}.build(t)

	ar, err := OpenArchive(path)
	require.NoError(t, err)

	header, err := ar.ReadHeader()
	require.NoError(t, err)

	assert.Equal(t, "my-app", header.Manifest.Package.Name)
	assert.Equal(t, "1.0.0", header.Version)
	assert.True(t, header.WithDeps)
	assert.Equal(t, "3.0.5", header.Lock.BundledTarantool)
}

// TestReadHeaderWithoutDeps reports a runtime-less archive as without-deps.
func TestReadHeaderWithoutDeps(t *testing.T) {
	t.Parallel()

	path := archiveSpec{name: "some-lib", version: "2.0.0"}.build(t)

	ar, err := OpenArchive(path)
	require.NoError(t, err)

	header, err := ar.ReadHeader()
	require.NoError(t, err)

	assert.False(t, header.WithDeps)
	assert.Empty(t, header.Lock.BundledTarantool)
}

// TestExtractProjectKeepsRocksPrefix extracts into a project root and checks the
// .rocks/ prefix survives while root metadata does not leak into the tree.
func TestExtractProjectKeepsRocksPrefix(t *testing.T) {
	t.Parallel()

	path := archiveSpec{
		name: "my-app", version: "1.0.0", withRuntime: "3.0.5",
		files: rockFiles("my-app", "1.0.0"),
	}.build(t)

	ar, err := OpenArchive(path)
	require.NoError(t, err)

	root := t.TempDir()
	mapper := extractMapper(ScopeProject, nil, true)
	require.NoError(t, ar.Extract(root, mapper))

	assert.FileExists(t, filepath.Join(root, ".rocks", "share", "tarantool", "my-app", "init.lua"))
	assert.FileExists(t, filepath.Join(root, "_runtime", "tarantool", "bin", "tarantool"))

	// Root metadata must not be laid into the tree.
	assert.NoFileExists(t, filepath.Join(root, "app.manifest.toml"))
	assert.NoFileExists(t, filepath.Join(root, "VERSION"))
}

// TestExtractSkipsDependency verifies a dependency in the skip set is not
// written from the archive.
func TestExtractSkipsDependency(t *testing.T) {
	t.Parallel()

	path := archiveSpec{
		name: "my-app", version: "1.0.0", withRuntime: "3.0.5",
		files: mergeFiles(rockFiles("my-app", "1.0.0"), rockFiles("luasocket", "3.0.4")),
	}.build(t)

	ar, err := OpenArchive(path)
	require.NoError(t, err)

	root := t.TempDir()
	skip := map[string]struct{}{"luasocket": {}}
	require.NoError(t, ar.Extract(root, extractMapper(ScopeProject, skip, true)))

	assert.FileExists(t, filepath.Join(root, ".rocks", "share", "tarantool", "my-app", "init.lua"))
	assert.NoFileExists(t,
		filepath.Join(root, ".rocks", "share", "tarantool", "luasocket", "init.lua"))
}

// TestExtractRejectsTraversal guards the tar-slip defense: an entry climbing out
// of the destination is refused and nothing is written.
func TestExtractRejectsTraversal(t *testing.T) {
	t.Parallel()

	dst := filepath.Join(t.TempDir(), "evil.tt")
	writeRawTarZst(t, dst, "../escape.txt", "pwned")

	ar, err := OpenArchive(dst)
	require.NoError(t, err)

	root := t.TempDir()

	err = ar.Extract(root, nil)
	require.ErrorIs(t, err, errUnsafePath)

	assert.NoFileExists(t, filepath.Join(filepath.Dir(root), "escape.txt"))
}

// writeRawTarZst writes a single-entry tar+zstd stream with an arbitrary (even
// unsafe) name, for the traversal guard test.
func writeRawTarZst(t *testing.T, dst, name, content string) {
	t.Helper()

	f, err := os.Create(dst) //nolint:gosec // Test writes to a temp path.
	require.NoError(t, err)

	defer func() { require.NoError(t, f.Close()) }()

	zstdWriter, err := zstd.NewWriter(f)
	require.NoError(t, err)

	tarWriter := tar.NewWriter(zstdWriter)
	require.NoError(t, tarWriter.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: name, Mode: 0o644,
		Size: int64(len(content)), Format: tar.FormatPAX,
	}))

	_, err = tarWriter.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, tarWriter.Close())
	require.NoError(t, zstdWriter.Close())
}
