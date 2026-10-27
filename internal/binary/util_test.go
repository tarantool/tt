package binary_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestTarantoolBuildPath(t *testing.T) {
	for _, test := range []struct {
		name    string
		target  string
		isBuild bool
	}{
		{"legacy installation", "tarantool_3.8.0", false},
		{"CE installation", "../versions/tarantool/tarantool_3.8.0/tarantool", false},
		{"EE installation", "../versions/tarantool-ee/tarantool-ee_3.8.0/tarantool", false},
		{"SDK installation",
			"../versions/tarantool-ee/tarantool_sdk_gc64-3.8.0-0-r94/tarantool", false},
		{"local build", "../build/src/tarantool", true},
		{"local build under versions", "../versions/tarantool/build/src/tarantool", true},
		{"other scope", "../other/versions/tarantool/tarantool_3.8.0/tarantool", true},
		{"empty version", "../versions/tarantool/tarantool_/tarantool", true},
		{"wrong edition", "../versions/tarantool/tarantool-ee_3.8.0/tarantool", true},
		{"wrong product", "../versions/tt/tt_3.8.0/tarantool", true},
		{"wrong executable", "../versions/tarantool/tarantool_3.8.0/other", true},
		{"wrong directory", "../build/tarantool/tarantool_3.8.0/tarantool", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			binDir := filepath.Join(root, binary.BinDirName)
			require.NoError(t, os.Mkdir(binDir, 0o755))

			link := filepath.Join(binDir, "tarantool")
			target := filepath.Join(binDir, filepath.FromSlash(test.target))

			// Repeat with existing and missing targets, and both link forms.
			for _, existing := range []bool{false, true} {
				if existing {
					require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
					require.NoError(t, os.WriteFile(target, nil, 0o755))
				}

				for _, linkTarget := range []string{filepath.FromSlash(test.target), target} {
					require.NoError(t, os.Symlink(linkTarget, link))

					build, err := binary.TarantoolBuildPath(binDir)
					require.NoError(t, err)

					if test.isBuild {
						assert.Equal(t, target, build)
					} else {
						assert.Empty(t, build)
					}

					require.NoError(t, os.Remove(link))
				}
			}
		})
	}
}

func TestCreateSymlink(t *testing.T) {
	tempDir := t.TempDir()
	targetFile, err := os.Create(filepath.Join(tempDir, "tgtFile.txt"))
	require.NoError(t, err)

	_ = targetFile.Close()

	// No overwrite.
	require.NoError(t, binary.CreateSymlink(targetFile.Name(), filepath.Join(tempDir, "first_link"),
		false))
	assert.FileExists(t, filepath.Join(tempDir, "first_link"))

	targetPath, err := os.Readlink(filepath.Join(tempDir, "first_link"))
	require.NoError(t, err)
	assert.Equal(t, targetFile.Name(), targetPath)

	// Overwrite flag is set, but symlink does not exist.
	require.NoError(t, binary.CreateSymlink(
		targetFile.Name(), filepath.Join(tempDir, "second_link"), true))
	assert.FileExists(t, filepath.Join(tempDir, "second_link"))

	targetPath, err = os.Readlink(filepath.Join(tempDir, "second_link"))
	require.NoError(t, err)
	assert.Equal(t, targetFile.Name(), targetPath)

	// Overwrite existing symlink.
	require.NoError(t, binary.CreateSymlink("./tgtFile.txt", filepath.Join(tempDir, "first_link"),
		true))
	assert.FileExists(t, filepath.Join(tempDir, "first_link"))

	targetPath, err = os.Readlink(filepath.Join(tempDir, "first_link"))
	require.NoError(t, err)
	assert.Equal(t, "./tgtFile.txt", targetPath)

	// Don't overwrite existing.
	require.Error(t, binary.CreateSymlink("./some_file", filepath.Join(tempDir, "first_link"),
		false))
	// Check existing link is not updated.
	assert.FileExists(t, filepath.Join(tempDir, "first_link"))

	targetPath, err = os.Readlink(filepath.Join(tempDir, "first_link"))
	require.NoError(t, err)
	assert.Equal(t, "./tgtFile.txt", targetPath)

	// A dangling link still exists and must obey the overwrite flag.
	require.NoError(t, os.Remove(targetFile.Name()))
	require.ErrorContains(t, binary.CreateSymlink("./new_target",
		filepath.Join(tempDir, "first_link"), false), "already exists")
	require.NoError(t, binary.CreateSymlink("./new_target",
		filepath.Join(tempDir, "first_link"), true))

	targetPath, err = os.Readlink(filepath.Join(tempDir, "first_link"))
	require.NoError(t, err)
	assert.Equal(t, "./new_target", targetPath)
}
