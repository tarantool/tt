package deps

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/resolve"
)

// baseManifest is a minimal valid manifest with one declared dependency and a
// comment above it. The comment is load-bearing in more than one test: the
// whole point of the position-preserving editor is that it survives.
const baseManifest = `manifest_version = '0.1'

[package]
name = 'my-app'
description = 'a test package'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

# checks validates the config; do not drop it.
[dependencies]
checks = '>=3.0.0'

[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'
`

// fakeResolver stands in for resolve.Engine so the pin selection can be
// asserted without a registry.
//
// It reproduces the one thing about the engine these tests depend on: the lock
// it returns is stamped with the hash of the manifest it was handed. That is
// what makes the re-parse invariant observable — hand it the pre-edit manifest
// and the lock carries the pre-edit hash, exactly as the real engine would.
type fakeResolver struct {
	// pins records the pin set of every call, in order.
	pins []resolve.Pins
	// seen records the manifest each call was given.
	seen []*manifest.Manifest
	// lock is the closure every call returns; nil means an empty one.
	lock *manifest.Lock
	// warnings is returned alongside it.
	warnings []string
	// err, when set, fails every call.
	err error
}

func (f *fakeResolver) ResolvePinned(
	_ context.Context, man *manifest.Manifest, pins resolve.Pins,
) (*manifest.Lock, []string, error) {
	f.pins = append(f.pins, pins)
	f.seen = append(f.seen, man)

	if f.err != nil {
		return nil, nil, f.err
	}

	out := manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: "0.1",
		GeneratedBy:     "tt test",
		ManifestHash:    man.Hash(),
	}

	if f.lock != nil {
		out.Products = f.lock.Products
	}

	return &out, f.warnings, nil
}

// lockOf builds a lock whose default product pins the given rocks.
func lockOf(pins map[string]string) *manifest.Lock {
	deps := make([]manifest.LockDependency, 0, len(pins))

	for _, name := range sortedKeys(pins) {
		deps = append(deps, manifest.LockDependency{
			Name:    name,
			Version: pins[name],
			Source:  "registry",
		})
	}

	return &manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: "0.1",
		GeneratedBy:     "tt test",
		ManifestHash:    "sha256:stale",
		Products:        map[string]manifest.LockProduct{"default": {Dependencies: deps}},
	}
}

// writeProject lays a manifest and, optionally, a lock into a fresh directory.
func writeProject(t *testing.T, source string, lock *manifest.Lock) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, manifestFileName), []byte(source), 0o600))

	if lock != nil {
		data, err := lock.Marshal()
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, lockFileName), data, 0o600))
	}

	return dir
}

// optsFor builds Options against a project directory.
func optsFor(dir string) Options {
	return Options{ProjectDir: dir, TtVersion: "tt test"}
}

// manifestOnDisk reads back the manifest source.
func manifestOnDisk(t *testing.T, dir string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, manifestFileName)) //nolint:gosec // temp path
	require.NoError(t, err)

	return string(data)
}

// lockOnDisk reads back and parses the lock.
func lockOnDisk(t *testing.T, dir string) *manifest.Lock {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, lockFileName)) //nolint:gosec // temp path
	require.NoError(t, err)

	lock, err := manifest.ParseLock(data)
	require.NoError(t, err)

	return lock
}
