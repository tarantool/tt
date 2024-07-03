package replicaset

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/running"
)

func Test_cleanDataFiles(t *testing.T) {
	tmpDir := t.TempDir()

	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "000.xlog"), []byte{}, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "000.snap"), []byte{}, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "000.vylog"), []byte{}, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "000.txt"), []byte{}, 0755))
	require.NoError(t, os.Mkdir(
		filepath.Join(tmpDir, "dir.snap"), 0755))

	require.NoError(t, cleanDataFiles(running.InstanceCtx{
		WalDir:   tmpDir,
		MemtxDir: tmpDir,
		VinylDir: tmpDir}))
	assert.NoFileExists(t, filepath.Join(tmpDir, "000.xlog"))
	assert.NoFileExists(t, filepath.Join(tmpDir, "000.snap"))
	assert.NoFileExists(t, filepath.Join(tmpDir, "000.vylog"))
	assert.FileExists(t, filepath.Join(tmpDir, "000.txt"))
	assert.DirExists(t, filepath.Join(tmpDir, "dir.snap"))
}
