package integrity_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/integrity"
)

// assertNoFile checks that there is no directory entry at path.
func assertNoFile(t *testing.T, path string) {
	t.Helper()

	_, err := os.Lstat(path)
	require.ErrorIs(t, err, os.ErrNotExist, "%s must not exist", path)
}

// writePackage writes the files of a package tree into base: a manifest,
// a lock, a template pair, Lua sources, a shared library, a program built
// into .rocks/bin and a file of no kind Tarantool loads.
func writePackage(t *testing.T, base string) {
	t.Helper()

	writeFile(t, filepath.Join(base, "app.manifest.toml"), "manifest\n", 0o644)
	writeFile(t, filepath.Join(base, "app.manifest.lock"), "lock\n", 0o644)
	writeFile(t, filepath.Join(base, "VERSION"), "1.0.0\n", 0o644)
	writeFile(t, filepath.Join(base, ".templates", "default", "config.yaml"), "config\n", 0o644)
	writeFile(t, filepath.Join(base, ".templates", "default", "instances.yaml"),
		"instances\n", 0o644)
	writeFile(t, filepath.Join(base, ".rocks", "share", "tarantool", "app", "init.lua"),
		"init\n", 0o644)
	writeFile(t, filepath.Join(base, ".rocks", "lib", "tarantool", "app", "B.so"), "B\n", 0o644)
	writeFile(t, filepath.Join(base, ".rocks", "lib", "tarantool", "app", "a.dylib"), "a\n", 0o644)
	writeFile(t, filepath.Join(base, ".rocks", "bin", "gw"), "gateway\n", 0o755)
	writeFile(t, filepath.Join(base, "run.lua"), "run\n", 0o755)
	writeFile(t, filepath.Join(base, "data", "notes.txt"), "notes\n", 0o644)
}

func TestRSASignerSignsPackageTree(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()

	writePackage(t, base)

	require.NoError(t, newSigner(t, key).Sign(base, integrity.SignOptions{}))

	// What Tarantool loads: Lua sources, shared libraries and cluster
	// configs, whatever their modes, depth-first in byte order.
	assert.Equal(t, hashesJSON(
		entry(".rocks/lib/tarantool/app/B.so", "B\n"),
		entry(".rocks/lib/tarantool/app/a.dylib", "a\n"),
		entry(".rocks/share/tarantool/app/init.lua", "init\n"),
		entry(".templates/default/config.yaml", "config\n"),
		entry("run.lua", "run\n"),
	), readFile(t, filepath.Join(base, "hashes.json")))

	// Everything else, the programs tt runs and the files it reads among
	// them; no tt.yaml is needed.
	assert.Equal(t, hashesJSON(
		entry(".rocks/bin/gw", "gateway\n"),
		entry(".templates/default/instances.yaml", "instances\n"),
		entry("VERSION", "1.0.0\n"),
		entry("app.manifest.lock", "lock\n"),
		entry("app.manifest.toml", "manifest\n"),
		entry("data/notes.txt", "notes\n"),
	), readFile(t, filepath.Join(base, "env_hashes.json")))

	for _, name := range []string{"hashes.json", "env_hashes.json"} {
		assertDigests(t, base, readSignedHashes(t, key, filepath.Join(base, name)))
	}

	ctx, err := initializeCheck(t, key, base)
	require.NoError(t, err)
	require.NoError(t, ctx.Repository.ValidateAll())

	for _, name := range []string{"app.manifest.toml", ".rocks/bin/gw", "run.lua"} {
		_, err := readThrough(t, ctx.Repository, filepath.Join(base, name))
		require.NoError(t, err, name)
	}

	require.NoError(t, integrity.NewRSAProvider().VerifyTree(
		writePublicKey(t, t.TempDir(), key), base))
}

func TestRSASignerSkipsAndLoads(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()

	writeFile(t, filepath.Join(base, "app.manifest.toml"), "manifest\n", 0o644)
	writeFile(t, filepath.Join(base, "bin", "start"), "start script\n", 0o644)
	writeFile(t, filepath.Join(base, "_runtime", "tt", "bin", "tt"), "tt\n", 0o755)
	writeFile(t, filepath.Join(base, ".rocks", "manifests", "guest", "state"), "state\n", 0o644)
	writeFile(t, filepath.Join(base, ".rocks", "manifests.lua"), "not skipped\n", 0o644)

	require.NoError(t, newSigner(t, key).Sign(base, integrity.SignOptions{
		Skip:   []string{"_runtime", ".rocks/manifests"},
		Loaded: []string{"bin/start"},
	}))

	assert.Equal(t, hashesJSON(
		entry(".rocks/manifests.lua", "not skipped\n"),
		entry("bin/start", "start script\n"),
	), readFile(t, filepath.Join(base, "hashes.json")))
	assert.Equal(t, hashesJSON(entry("app.manifest.toml", "manifest\n")),
		readFile(t, filepath.Join(base, "env_hashes.json")))

	t.Run("loaded file missing", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "init.lua"), "init\n", 0o644)

		err := newSigner(t, key).Sign(dir, integrity.SignOptions{Loaded: []string{"run"}})
		require.ErrorContains(t, err, `"run" is not a file of the tree`)
		assertNoFile(t, filepath.Join(dir, "hashes.json"))
		assertNoFile(t, filepath.Join(dir, "env_hashes.json"))
	})

	t.Run("loaded file skipped", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "_runtime", "run"), "run\n", 0o644)

		err := newSigner(t, key).Sign(dir, integrity.SignOptions{
			Skip: []string{"_runtime"}, Loaded: []string{"_runtime/run"},
		})
		require.ErrorContains(t, err, "not a file of the tree")
	})
}

func TestRSASignerNeverListsItsOutputs(t *testing.T) {
	key := signingKey(t)
	outputs := []string{"hashes.json", "hashes.json.sig", "env_hashes.json", "env_hashes.json.sig"}

	for _, name := range outputs {
		t.Run("nested "+name, func(t *testing.T) {
			base := t.TempDir()

			writeFile(t, filepath.Join(base, "init.lua"), "init\n", 0o644)
			writeFile(t, filepath.Join(base, "sub", name), "nested\n", 0o755)
			writeFile(t, filepath.Join(base, "sub", "tool"), "tool\n", 0o755)

			require.NoError(t, newSigner(t, key).Sign(base, integrity.SignOptions{}))
			assert.Equal(t, hashesJSON(entry("init.lua", "init\n")),
				readFile(t, filepath.Join(base, "hashes.json")))
			assert.Equal(t, hashesJSON(entry("sub/tool", "tool\n")),
				readFile(t, filepath.Join(base, "env_hashes.json")))
		})
	}

	// A signed tree signs again into the same lists, and the result checks.
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "init.lua"), "init\n", 0o644)
	writeFile(t, filepath.Join(base, "VERSION"), "1\n", 0o644)

	require.NoError(t, newSigner(t, key).Sign(base, integrity.SignOptions{}))
	require.NoError(t, newSigner(t, key).Sign(base, integrity.SignOptions{}))

	assert.Equal(t, hashesJSON(entry("init.lua", "init\n")),
		readFile(t, filepath.Join(base, "hashes.json")))
	assert.Equal(t, hashesJSON(entry("VERSION", "1\n")),
		readFile(t, filepath.Join(base, "env_hashes.json")))

	ctx, err := initializeCheck(t, key, base)
	require.NoError(t, err)
	require.NoError(t, ctx.Repository.ValidateAll())
}

func TestRSASignerTreeWithoutFiles(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()

	require.NoError(t, newSigner(t, key).Sign(base, integrity.SignOptions{}))

	// Exactly {"files":[]}: an empty array, never null.
	assert.Equal(t, hashesJSON(), readFile(t, filepath.Join(base, "hashes.json")))
	assert.Equal(t, hashesJSON(), readFile(t, filepath.Join(base, "env_hashes.json")))
	assert.Empty(t, readSignedHashes(t, key, filepath.Join(base, "env_hashes.json")))
}

func TestRSASignerRefusesTrees(t *testing.T) {
	key := signingKey(t)

	t.Run("missing", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")

		err := newSigner(t, key).Sign(dir, integrity.SignOptions{})
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "file")
		writeFile(t, dir, "not a directory\n", 0o644)

		err := newSigner(t, key).Sign(dir, integrity.SignOptions{})
		require.ErrorContains(t, err, dir)
		require.ErrorContains(t, err, "not a directory")
	})
}

func TestRSASignerSymbolicLinks(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()

	writeFile(t, filepath.Join(base, "data", "notes.txt"), "notes\n", 0o644)
	writeFile(t, filepath.Join(base, "data", "lib.txt"), "library\n", 0o644)
	// A link is listed under its own name, with the content of its target,
	// and its name says what loads it.
	symlink(t, filepath.Join("data", "lib.txt"), filepath.Join(base, "lib.lua"))
	symlink(t, filepath.Join("data", "notes.txt"), filepath.Join(base, "notes"))
	// Links to directories are neither descended into nor listed.
	symlink(t, "data", filepath.Join(base, "linked"))
	symlink(t, "data", filepath.Join(base, "linked.lua"))

	require.NoError(t, newSigner(t, key).Sign(base, integrity.SignOptions{}))

	assert.Equal(t, hashesJSON(entry("lib.lua", "library\n")),
		readFile(t, filepath.Join(base, "hashes.json")))
	assert.Equal(t, hashesJSON(
		entry("data/lib.txt", "library\n"),
		entry("data/notes.txt", "notes\n"),
		entry("notes", "notes\n"),
	), readFile(t, filepath.Join(base, "env_hashes.json")))

	ctx, err := initializeCheck(t, key, base)
	require.NoError(t, err)

	data, err := readThrough(t, ctx.Repository, filepath.Join(base, "lib.lua"))
	require.NoError(t, err)
	assert.Equal(t, "library\n", data)
}

func TestRSASignerDanglingSymbolicLinks(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()
	link := filepath.Join(base, "sub", "broken.lua")

	writeFile(t, filepath.Join(base, "init.lua"), "init\n", 0o644)
	symlink(t, filepath.Join(base, "missing"), link)

	// A link that does not resolve fails, and no hashes file is written.
	err := newSigner(t, key).Sign(base, integrity.SignOptions{})
	require.ErrorContains(t, err, link)
	assertNoFile(t, filepath.Join(base, "hashes.json"))
	assertNoFile(t, filepath.Join(base, "env_hashes.json"))

	// In a skipped directory it is not looked at.
	require.NoError(t, newSigner(t, key).Sign(base, integrity.SignOptions{Skip: []string{"sub"}}))
}

func TestRSASignerReportsWriteFailures(t *testing.T) {
	key := signingKey(t)

	// A directory in the place of a file makes writing it fail whoever
	// runs the test.
	t.Run("hashes file", func(t *testing.T) {
		base := t.TempDir()
		hashes := filepath.Join(base, "hashes.json")

		writeFile(t, filepath.Join(base, "init.lua"), "init\n", 0o644)
		require.NoError(t, os.MkdirAll(hashes, 0o755))

		err := newSigner(t, key).Sign(base, integrity.SignOptions{})
		require.ErrorContains(t, err, hashes)
		assertNoFile(t, hashes+".sig")
		assertNoFile(t, filepath.Join(base, "env_hashes.json"))
	})

	t.Run("signature", func(t *testing.T) {
		base := t.TempDir()
		hashes := filepath.Join(base, "env_hashes.json")

		writeFile(t, filepath.Join(base, "VERSION"), "1\n", 0o644)
		require.NoError(t, os.MkdirAll(hashes+".sig", 0o755))

		err := newSigner(t, key).Sign(base, integrity.SignOptions{})
		require.ErrorContains(t, err, hashes+".sig")

		// The hashes file and its signature are not replaced atomically.
		assert.Equal(t, hashesJSON(entry("VERSION", "1\n")), readFile(t, hashes))
	})
}

func TestRSASignerSignFiles(t *testing.T) {
	key := signingKey(t)
	source := t.TempDir()
	dir := t.TempDir()

	writeFile(t, filepath.Join(source, "bin", "tarantool"), "tarantool\n", 0o755)
	writeFile(t, filepath.Join(source, "include", "tarantool", "module.h"), "header\n", 0o644)

	// The files are listed under the paths they are to have in dir, in the
	// order of those paths, whatever the paths they come from.
	require.NoError(t, newSigner(t, key).SignFiles(dir, []integrity.SignedFile{
		{Path: "tarantool", Source: filepath.Join(source, "bin", "tarantool")},
		{
			Path:   "include/tarantool/module.h",
			Source: filepath.Join(source, "include", "tarantool", "module.h"),
		},
	}))

	hashes := filepath.Join(dir, "env_hashes.json")
	assert.Equal(t, hashesJSON(
		entry("include/tarantool/module.h", "header\n"),
		entry("tarantool", "tarantool\n"),
	), readFile(t, hashes))
	assertNoFile(t, filepath.Join(dir, "hashes.json"))

	// Once laid out so, the tree checks.
	writeFile(t, filepath.Join(dir, "tarantool"), "tarantool\n", 0o755)
	writeFile(t, filepath.Join(dir, "include", "tarantool", "module.h"), "header\n", 0o644)

	publicKey := writePublicKey(t, t.TempDir(), key)
	require.NoError(t, integrity.NewRSAProvider().VerifyTree(publicKey, dir))

	for _, bad := range []string{"", "/abs", "a/../b", "./a", "a//b", "a/", "..", `a\b`} {
		err := newSigner(t, key).SignFiles(t.TempDir(), []integrity.SignedFile{
			{Path: bad, Source: filepath.Join(source, "bin", "tarantool")},
		})
		require.ErrorContains(t, err, "invalid path to list", bad)
	}

	err := newSigner(t, key).SignFiles(t.TempDir(), []integrity.SignedFile{
		{Path: "tarantool", Source: filepath.Join(source, "bin", "tarantool")},
		{Path: "tarantool", Source: filepath.Join(source, "bin", "tarantool")},
	})
	require.ErrorContains(t, err, "listed twice")

	err = newSigner(t, key).SignFiles(t.TempDir(), []integrity.SignedFile{
		{Path: "tarantool", Source: filepath.Join(source, "missing")},
	})
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRSAVerifyTree(t *testing.T) {
	key := signingKey(t)
	publicKey := writePublicKey(t, t.TempDir(), key)
	provider := integrity.NewRSAProvider()

	signed := func(t *testing.T) string {
		t.Helper()

		base := t.TempDir()
		writePackage(t, base)
		require.NoError(t, newSigner(t, key).Sign(base, integrity.SignOptions{}))

		return base
	}

	require.NoError(t, provider.VerifyTree(publicKey, signed(t)))

	t.Run("unsigned", func(t *testing.T) {
		base := t.TempDir()
		writePackage(t, base)

		err := provider.VerifyTree(publicKey, base)
		require.ErrorContains(t, err, "not signed")
	})

	t.Run("tampered", func(t *testing.T) {
		base := signed(t)
		writeFile(t, filepath.Join(base, ".rocks", "bin", "gw"), "tampered\n", 0o755)

		require.ErrorContains(t, provider.VerifyTree(publicKey, base), "hash mismatch")
	})

	t.Run("file added", func(t *testing.T) {
		base := signed(t)
		added := filepath.Join(base, ".rocks", "share", "tarantool", "app", "evil.lua")
		writeFile(t, added, "evil\n", 0o644)

		err := provider.VerifyTree(publicKey, base)
		require.ErrorContains(t, err, "not listed in the hashes files")
		require.ErrorContains(t, err, added)
	})

	t.Run("foreign key", func(t *testing.T) {
		base := t.TempDir()
		writePackage(t, base)
		require.NoError(t, newSigner(t, foreignKey(t)).Sign(base, integrity.SignOptions{}))

		require.ErrorContains(t, provider.VerifyTree(publicKey, base), "verification error")
	})

	t.Run("disabled", func(t *testing.T) {
		err := integrity.NewDisabledProvider().VerifyTree(publicKey, signed(t))
		require.ErrorIs(t, err, integrity.ErrNoVerifierInCE)
	})
}

func TestRSACheckSeveralRoots(t *testing.T) {
	key := signingKey(t)
	app := t.TempDir()
	runtime := t.TempDir()
	unsigned := t.TempDir()

	writePackage(t, app)
	require.NoError(t, newSigner(t, key).Sign(app, integrity.SignOptions{}))

	writeFile(t, filepath.Join(runtime, "tarantool"), "tarantool\n", 0o755)
	require.NoError(t, newSigner(t, key).SignFiles(runtime, []integrity.SignedFile{
		{Path: "tarantool", Source: filepath.Join(runtime, "tarantool")},
	}))

	writeFile(t, filepath.Join(unsigned, "tt"), "tt\n", 0o755)

	publicKey := writePublicKey(t, t.TempDir(), key)

	// A root named twice is checked once; a root without hashes files lists
	// nothing.
	ctx, err := integrity.NewRSAProvider().InitializeIntegrityCheck(publicKey,
		app, runtime, app, unsigned)
	require.NoError(t, err)

	for _, path := range []string{
		filepath.Join(app, "app.manifest.toml"),
		filepath.Join(runtime, "tarantool"),
	} {
		_, err := readThrough(t, ctx.Repository, path)
		require.NoError(t, err, path)
	}

	_, err = readThrough(t, ctx.Repository, filepath.Join(unsigned, "tt"))
	require.ErrorContains(t, err, "not in repository")

	writeFile(t, filepath.Join(runtime, "tarantool"), "tampered\n", 0o755)
	require.ErrorContains(t, ctx.Repository.ValidateAll(), "hash mismatch")
}
