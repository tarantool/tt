//go:build linux || darwin

package integrity_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/integrity"
)

// mkfifo creates a FIFO at path with mode whatever the umask, creating its
// directory.
func mkfifo(t *testing.T, path string, mode os.FileMode) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, syscall.Mkfifo(path, uint32(mode)))
	require.NoError(t, os.Chmod(path, mode))
}

// setUmask sets the umask of the process until the end of the test.
func setUmask(t *testing.T, mask int) {
	t.Helper()

	previous := syscall.Umask(mask)

	t.Cleanup(func() { syscall.Umask(previous) })
}

// permissions returns the permission bits of the file at path.
func permissions(t *testing.T, path string) os.FileMode {
	t.Helper()

	info, err := os.Stat(path)
	require.NoError(t, err)

	return info.Mode().Perm()
}

func TestRSASignerRefusesSpecialFiles(t *testing.T) {
	key := signingKey(t)

	// A FIFO fails the signing wherever it is in the tree: every file of a
	// package is listed.
	for _, fifo := range []string{"pipe.lua", "sub/pipe", ".rocks/bin/pipe"} {
		t.Run(fifo, func(t *testing.T) {
			base := t.TempDir()
			path := filepath.Join(base, filepath.FromSlash(fifo))

			writeFile(t, filepath.Join(base, "init.lua"), "init\n", 0o644)
			mkfifo(t, path, 0o644)

			err := newSigner(t, key).Sign(base, integrity.SignOptions{})
			require.ErrorContains(t, err, path)
			assertNoFile(t, filepath.Join(base, "hashes.json"))
			assertNoFile(t, filepath.Join(base, "env_hashes.json"))
		})
	}

	t.Run("link to a FIFO", func(t *testing.T) {
		base := t.TempDir()
		link := filepath.Join(base, "link.lua")

		mkfifo(t, filepath.Join(base, "skipped", "pipe"), 0o644)
		symlink(t, filepath.Join("skipped", "pipe"), link)

		err := newSigner(t, key).Sign(base, integrity.SignOptions{Skip: []string{"skipped"}})
		require.ErrorContains(t, err, link)
	})

	t.Run("FIFO in a skipped directory", func(t *testing.T) {
		base := t.TempDir()

		writeFile(t, filepath.Join(base, "init.lua"), "init\n", 0o644)
		mkfifo(t, filepath.Join(base, "skipped", "pipe"), 0o644)

		require.NoError(t, newSigner(t, key).Sign(base,
			integrity.SignOptions{Skip: []string{"skipped"}}))
		assert.Equal(t, hashesJSON(entry("init.lua", "init\n")),
			readFile(t, filepath.Join(base, "hashes.json")))
	})

	t.Run("listed by SignFiles", func(t *testing.T) {
		base := t.TempDir()
		fifo := filepath.Join(base, "pipe")
		mkfifo(t, fifo, 0o755)

		err := newSigner(t, key).SignFiles(t.TempDir(), []integrity.SignedFile{
			{Path: "tarantool", Source: fifo},
		})
		require.ErrorContains(t, err, fifo)
	})
}

func TestRSASignerFileModes(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()

	writeFile(t, filepath.Join(base, "init.lua"), "init\n", 0o644)
	writeFile(t, filepath.Join(base, "VERSION"), "1\n", 0o644)

	// Without a umask the creation modes are the modes of the files.
	setUmask(t, 0)
	require.NoError(t, newSigner(t, key).Sign(base, integrity.SignOptions{}))

	written := []string{"env_hashes.json", "hashes.json"}
	for _, name := range written {
		path := filepath.Join(base, name)
		assert.Equal(t, os.FileMode(0o644), permissions(t, path), name)
		assert.Equal(t, os.FileMode(0o644), permissions(t, path+".sig"), name+".sig")
	}

	// Existing files are overwritten in place and keep their modes.
	for _, name := range written {
		path := filepath.Join(base, name)
		writeFile(t, path, "stale", 0o600)
		writeFile(t, path+".sig", "stale", 0o640)
	}

	require.NoError(t, newSigner(t, key).Sign(base, integrity.SignOptions{}))

	for _, name := range written {
		path := filepath.Join(base, name)
		assert.Equal(t, os.FileMode(0o600), permissions(t, path), name)
		assert.Equal(t, os.FileMode(0o640), permissions(t, path+".sig"), name+".sig")
		assertDigests(t, base, readSignedHashes(t, key, path))
	}
}

func TestRSASignerFileModesUnderUmask(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()

	writeFile(t, filepath.Join(base, "init.lua"), "init\n", 0o644)

	// The creation modes are subject to the umask: it takes the write bit
	// of the group and every bit of others away. A file forced to 0644
	// would keep bits it removes.
	setUmask(t, 0o027)
	require.NoError(t, newSigner(t, key).Sign(base, integrity.SignOptions{}))

	for _, name := range []string{"env_hashes.json", "hashes.json"} {
		path := filepath.Join(base, name)
		assert.Equal(t, os.FileMode(0o640), permissions(t, path), name)
		assert.Equal(t, os.FileMode(0o640), permissions(t, path+".sig"), name+".sig")
	}
}

func TestRSAProviderOpensFewFiles(t *testing.T) {
	const (
		limit = 64
		count = 4 * limit
	)

	key := signingKey(t)
	base := t.TempDir()
	ttYAML := filepath.Join(base, "tt.yaml")

	writeFile(t, ttYAML, "env: {}\n", 0o644)

	for index := range count {
		dir := filepath.Join(base, fmt.Sprintf("dir%d", index%8))
		writeFile(t, filepath.Join(dir, fmt.Sprintf("f%d.lua", index)),
			fmt.Sprintf("file %d\n", index), 0o644)
		writeFile(t, filepath.Join(base, "bin", fmt.Sprintf("tool%d", index)),
			fmt.Sprintf("tool %d\n", index), 0o644)
	}

	signer := newSigner(t, key)
	publicKey := writePublicKey(t, t.TempDir(), key)

	var original syscall.Rlimit

	require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_NOFILE, &original))

	lowered := original

	lowered.Cur = limit

	require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lowered))
	t.Cleanup(func() { require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_NOFILE, &original)) })

	// The limit is in force: the process cannot hold that many files.
	var opened []*os.File

	var openErr error

	for range limit {
		file, err := os.Open(ttYAML)
		if err != nil {
			openErr = err

			break
		}

		opened = append(opened, file)
	}

	for _, file := range opened {
		require.NoError(t, file.Close())
	}

	require.ErrorIs(t, openErr, syscall.EMFILE)

	// A file left open must count until the end: the finalizers of the
	// collector would close it at a time of their own.
	previousGC := debug.SetGCPercent(-1)

	t.Cleanup(func() { debug.SetGCPercent(previousGC) })

	require.NoError(t, signer.Sign(base, integrity.SignOptions{}))

	ctx, err := integrity.NewRSAProvider().InitializeIntegrityCheck(publicKey, base)
	require.NoError(t, err)
	require.NoError(t, ctx.Repository.ValidateAll())
}

func TestRSASignerRefusesNamesThatAreNotUTF8(t *testing.T) {
	base := t.TempDir()
	name := "name\xff.lua"

	err := os.WriteFile(filepath.Join(base, name), []byte("script\n"), 0o644)
	if err != nil {
		t.Skipf("the file system refuses a name that is not UTF-8: %v", err)
	}

	// JSON cannot carry the name: writing it would list another file.
	err = newSigner(t, signingKey(t)).Sign(base, integrity.SignOptions{})
	require.ErrorContains(t, err, "UTF-8")
	assertNoFile(t, filepath.Join(base, "hashes.json"))
}
