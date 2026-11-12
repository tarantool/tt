package pack

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extraIDs lists the IDs of the extra fields of extra.
func extraIDs(extra []byte) []uint16 {
	var ids []uint16

	for len(extra) >= extraHeaderLen {
		ids = append(ids, binary.LittleEndian.Uint16(extra))
		extra = extra[extraHeaderLen+int(binary.LittleEndian.Uint16(extra[2:])):]
	}

	return ids
}

// assertZipEpoch checks that every entry of the zip archive at path is dated
// 1980-01-01 00:00 in its MS-DOS fields and has no extended timestamp.
func assertZipEpoch(t *testing.T, path string) {
	t.Helper()

	archive, err := zip.OpenReader(path)
	require.NoError(t, err)

	defer func() { _ = archive.Close() }()

	require.NotEmpty(t, archive.File)

	for _, file := range archive.File {
		assert.Equal(t, uint16(0x21), file.ModifiedDate, "%s: the DOS date", file.Name)
		assert.Equal(t, uint16(0), file.ModifiedTime, "%s: the DOS time", file.Name)
		assert.NotContains(t, extraIDs(file.Extra), uint16(extTimeExtraID), file.Name)
		assert.Equal(t, time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC),
			file.Modified, file.Name)
	}
}

// TestPackRocksAreDatedTheZipEpoch: both rocks date every entry 1980-01-01
// 00:00, whatever the time zone of the reader, and the binary rock is the same
// bytes from the same build.
func TestPackRocksAreDatedTheZipEpoch(t *testing.T) {
	t.Parallel()

	src := writeGitLibrary(t)

	pack := func(out string) *Result {
		result, err := Run(context.Background(),
			packOptions(src, filepath.Join(src, "_out", out), FormatRock, FormatRockSrc))
		require.NoError(t, err)

		return result
	}

	first := pack("a")

	assertZipEpoch(t, first.Rock)
	assertZipEpoch(t, first.SrcRock)

	second := pack("b")

	want, err := os.ReadFile(first.Rock)
	require.NoError(t, err)

	got, err := os.ReadFile(second.Rock)
	require.NoError(t, err)

	assert.Equal(t, want, got, "the binary rock is reproducible")

	// The entries keep their contents.
	assert.Equal(t, "return {}\n", zipEntries(t, first.Rock)["lua/mylib/init.lua"])
}

// TestPackRocksIntoTheDefaultOutputDir: with no output directory given, the
// rocks land in _build/pack, where PackStaged also has its temporary files,
// and stay there dated the zip epoch; nothing else of the pack is left
// beside them.
func TestPackRocksIntoTheDefaultOutputDir(t *testing.T) {
	t.Parallel()

	src := writeGitLibrary(t)

	result, err := Run(context.Background(), packOptions(src, "", FormatRock, FormatRockSrc))
	require.NoError(t, err)

	out := filepath.Join(src, buildDirName, packSubDir)
	assert.Equal(t, filepath.Join(out, "mylib-1.2.0-1.all.rock"), result.Rock)
	assert.Equal(t, filepath.Join(out, "mylib-1.2.0-1.src.rock"), result.SrcRock)

	require.FileExists(t, result.Rock)
	require.FileExists(t, result.SrcRock)
	assertZipEpoch(t, result.Rock)
	assertZipEpoch(t, result.SrcRock)

	dirs, err := os.ReadDir(out)
	require.NoError(t, err)

	for _, entry := range dirs {
		assert.False(t, entry.IsDir(), "%s is left in %s", entry.Name(), out)
	}
}

func TestWithoutExtTime(t *testing.T) {
	t.Parallel()

	field := func(fieldID, size uint16, data ...byte) []byte {
		out := binary.LittleEndian.AppendUint16(nil, fieldID)

		return append(binary.LittleEndian.AppendUint16(out, size), data...)
	}

	unix := field(0x7875, 6, 1, 4, 0, 0, 0, 0)
	extTime := field(extTimeExtraID, 5, 1, 0, 0, 0, 0)

	assert.Empty(t, withoutExtTime(nil))
	assert.Empty(t, withoutExtTime(extTime))
	assert.Equal(t, unix, withoutExtTime(append(append([]byte{}, extTime...), unix...)))
	assert.Equal(t, append(append([]byte{}, unix...), 0xff),
		withoutExtTime(append(append([]byte{}, unix...), 0xff)), "a field cut short is kept")
}
