package distbuild

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// TestReadLockChecksTheManifestHash covers the lock a distribution is built
// from: it is taken while it names the manifest by what the manifest says, so
// a comment is no change, and a lock of the scheme of an older tt, which
// hashed the bytes of the file, is out of date even for the very bytes.
func TestReadLockChecksTheManifestHash(t *testing.T) {
	t.Parallel()

	const source = "manifest_version = \"0.1\"\nmanifest_kind = \"tt-distribution\"\n"

	parse := func(src string) *manifest.Distribution {
		t.Helper()

		dist, _, err := manifest.ParseDistribution([]byte(src))
		require.NoError(t, err)

		return dist
	}

	dist := parse(source)
	commented := parse("# The smallest distribution.\n" + source)
	other := parse(source + "[package]\nname = \"tt-ee\"\n")

	writeLockFor := func(t *testing.T, hash string) string {
		t.Helper()

		dir := t.TempDir()

		lock := &manifest.DistributionLock{
			LockVersion:     manifest.LockVersion,
			ManifestVersion: manifest.ManifestVersion,
			GeneratedBy:     "tt 3.0.0",
			ManifestHash:    hash,
			Core: manifest.LockedCore{
				Module: "github.com/tarantool/tt/v3", Version: "v3.0.0",
			},
		}

		data, err := lock.Marshal()
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, manifest.DistributionLockFile), data, filePerm))
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, manifest.DistributionSumFile), []byte("sum\n"), filePerm))

		return dir
	}

	t.Run("a comment", func(t *testing.T) {
		t.Parallel()

		lock, sum, err := readLock(writeLockFor(t, dist.Hash()), commented, Options{Locked: true})
		require.NoError(t, err)
		require.NotNil(t, lock)
		assert.Equal(t, "sum\n", string(sum))
	})

	t.Run("another manifest", func(t *testing.T) {
		t.Parallel()

		dir := writeLockFor(t, dist.Hash())

		_, _, err := readLock(dir, other, Options{Locked: true})
		require.ErrorIs(t, err, errStaleLock)
		require.ErrorIs(t, err, manifest.ErrManifestChanged)

		lock, _, err := readLock(dir, other, Options{})
		require.NoError(t, err)
		assert.Nil(t, lock, "a build that may write the lock resolves anew")
	})

	t.Run("the scheme of an older tt", func(t *testing.T) {
		t.Parallel()

		sum := sha256.Sum256([]byte(source))

		_, _, err := readLock(writeLockFor(t, "sha256:"+hex.EncodeToString(sum[:])), dist,
			Options{Locked: true})
		require.ErrorIs(t, err, errStaleLock)
		require.ErrorIs(t, err, manifest.ErrHashOutdated)
		assert.Contains(t, err.Error(), "tt hashes the manifest differently now; "+
			"rebuild without --locked to update it")
	})
}

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
