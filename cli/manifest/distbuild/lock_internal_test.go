package distbuild

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// TestWriteLockUnchanged checks that a build writes the lock and the sum only
// when they pin something else than the ones next to the manifest: the tt
// that built it is not reason enough.
func TestWriteLockUnchanged(t *testing.T) {
	t.Parallel()

	bld := builder{dir: t.TempDir(), work: t.TempDir()}
	lockPath := filepath.Join(bld.dir, manifest.DistributionLockFile)
	sumPath := filepath.Join(bld.dir, manifest.DistributionSumFile)

	readLock := func() string {
		t.Helper()

		data, err := os.ReadFile(lockPath)
		require.NoError(t, err)

		return string(data)
	}

	writeSum := func(sum string) {
		t.Helper()

		require.NoError(t, os.WriteFile(filepath.Join(bld.work, "go.sum"), []byte(sum), filePerm))
	}

	writeSum("example.com/a v1.0.0 h1:a=\n")

	lock := &manifest.DistributionLock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
		GeneratedBy:     "tt 3.0.0",
		ManifestHash:    "sha256:00",
		Core: manifest.LockedCore{
			Module: "github.com/tarantool/tt/v3", Version: "v3.0.0",
		},
	}
	require.NoError(t, bld.writeLock(lock))

	first := readLock()
	assert.Contains(t, first, `generated_by = 'tt 3.0.0'`)

	// Another tt pins the same: neither file is touched.
	again := *lock

	again.GeneratedBy = "tt 3.1.0"
	require.NoError(t, bld.writeLock(&again))
	assert.Equal(t, first, readLock())

	// Another version of the core is written, with the tt that wrote it.
	again.Core.Version = "v3.0.1"
	require.NoError(t, bld.writeLock(&again))
	assert.Contains(t, readLock(), `version = 'v3.0.1'`)
	assert.Contains(t, readLock(), `generated_by = 'tt 3.1.0'`)

	// So is another go.sum.
	again.GeneratedBy = "tt 3.2.0"

	writeSum("example.com/a v1.0.1 h1:b=\n")
	require.NoError(t, bld.writeLock(&again))
	assert.Contains(t, readLock(), `generated_by = 'tt 3.2.0'`)

	sum, err := os.ReadFile(sumPath)
	require.NoError(t, err)
	assert.Equal(t, "example.com/a v1.0.1 h1:b=\n", string(sum))

	// A lock with a section this tt no longer writes is written anew.
	require.NoError(t, os.WriteFile(lockPath,
		[]byte(readLock()+"\n[build]\nssl = 'default'\n"), filePerm))

	again.GeneratedBy = "tt 3.3.0"
	require.NoError(t, bld.writeLock(&again))
	assert.NotContains(t, readLock(), "[build]")
	assert.Contains(t, readLock(), `generated_by = 'tt 3.3.0'`)
}
