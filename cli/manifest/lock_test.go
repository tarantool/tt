package manifest_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

func TestLockRoundTrip(t *testing.T) {
	t.Parallel()

	lock := &manifest.Lock{
		LockVersion:      manifest.LockVersion,
		ManifestVersion:  manifest.ManifestVersion,
		GeneratedBy:      "tt 3.1.0",
		ManifestHash:     "sha256:abc123",
		BundledTarantool: "3.0.5",
		BundledTt:        "",
		BundledTcm:       "",
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: []manifest.LockDependency{
				{
					Name:        "luasocket",
					Version:     "3.0.4",
					Source:      "registry",
					Path:        "",
					ContentHash: "",
				},
				{
					Name:        "local-helper",
					Version:     "0.1.0",
					Source:      "path",
					Path:        "../helper",
					ContentHash: "sha256:cafe",
				},
			}},
			"minimal": {Dependencies: []manifest.LockDependency{
				{
					Name:        "luasocket",
					Version:     "3.0.4",
					Source:      "registry",
					Path:        "",
					ContentHash: "",
				},
			}},
		},
	}

	out, err := lock.Marshal()
	require.NoError(t, err)

	// The on-disk shape uses the [lock.products.<name>] table tree.
	text := string(out)
	assert.Contains(t, text, "[lock.products.default]")
	assert.Contains(t, text, "[[lock.products.default.dependencies]]")
	assert.NotContains(t, text, "'lock.products'", "must not be a single quoted key")

	back, err := manifest.ParseLock(out)
	require.NoError(t, err)
	assert.Equal(t, lock.Products, back.Products)
	assert.Equal(t, lock.BundledTarantool, back.BundledTarantool)
	assert.Equal(t, "tt 3.1.0", back.GeneratedBy)
}

// TestLockDevDependenciesRoundTrip pins the dev closure's on-disk shape: one
// global [[lock.dev_dependencies]] array beside the per-product tables, both
// registry and path entries surviving a marshal/parse cycle intact.
func TestLockDevDependenciesRoundTrip(t *testing.T) {
	t.Parallel()

	lock := &manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
		GeneratedBy:     "tt 3.1.0",
		ManifestHash:    "sha256:abc123",
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: []manifest.LockDependency{
				{Name: "metrics", Version: "1.5.0-1", Source: "registry"},
			}},
		},
		DevDependencies: []manifest.LockDependency{
			{
				Name:    "luatest",
				Version: "1.0.1-1",
				Source:  "registry",
			},
			{
				Name:        "dev-helper",
				Version:     "0.1.0",
				Source:      "path",
				Path:        "../dev-helper",
				ContentHash: "sha256:cafe",
			},
		},
	}

	out, err := lock.Marshal()
	require.NoError(t, err)

	text := string(out)
	assert.Contains(t, text, "[[lock.dev_dependencies]]")
	assert.Contains(t, text, "[lock.products.default]")
	assert.NotContains(t, text, "'lock.dev_dependencies'", "must not be a single quoted key")

	back, err := manifest.ParseLock(out)
	require.NoError(t, err)
	assert.Equal(t, lock.DevDependencies, back.DevDependencies)
	assert.Equal(t, lock.Products, back.Products)
}

// TestLockWithoutDevDependenciesOmitsKey pins that a lock with no dev closure
// carries no dev_dependencies key at all, so locks written before dev
// dependencies were resolved keep marshaling byte-for-byte as they did.
func TestLockWithoutDevDependenciesOmitsKey(t *testing.T) {
	t.Parallel()

	lock := &manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
		GeneratedBy:     "tt 3.1.0",
		ManifestHash:    "sha256:abc123",
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: []manifest.LockDependency{
				{Name: "metrics", Version: "1.5.0-1", Source: "registry"},
			}},
		},
	}

	out, err := lock.Marshal()
	require.NoError(t, err)
	assert.NotContains(t, string(out), "dev_dependencies")

	back, err := manifest.ParseLock(out)
	require.NoError(t, err)
	assert.Empty(t, back.DevDependencies)
}

// TestLockKeepsAnEmptyProduct pins that a product resolved to no dependencies
// still has its entry after a write and a read. The staleness check treats a
// product with no entry as unresolved, so losing the empty entry on the way
// through the file would make every such lock stale forever.
func TestLockKeepsAnEmptyProduct(t *testing.T) {
	t.Parallel()

	lock := &manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
		GeneratedBy:     "tt 3.1.0",
		ManifestHash:    "sha256:abc123",
		Products: map[string]manifest.LockProduct{
			manifest.ImplicitProduct: {Dependencies: nil},
		},
	}

	out, err := lock.Marshal()
	require.NoError(t, err)

	back, err := manifest.ParseLock(out)
	require.NoError(t, err)
	assert.Contains(t, back.Products, manifest.ImplicitProduct)
	assert.Empty(t, back.Products[manifest.ImplicitProduct].Dependencies)
}

// TestLockArtifactsInline pins the on-disk shape of the digests a rock index
// gives: one inline table per dependency, keys sorted, and no key at all for
// a dependency without them, so a lock of a classic server marshals as it
// always has.
func TestLockArtifactsInline(t *testing.T) {
	t.Parallel()

	x86, arm, spec := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)

	lock := &manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
		GeneratedBy:     "tt 3.1.0",
		ManifestHash:    "sha256:abc123",
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: []manifest.LockDependency{
				{
					Name: "emmylua", Version: "0.30.0pre1-1", Source: "registry",
					Artifacts: map[string]string{
						"rockspec":       manifest.ArtifactDigest(spec),
						"macosx-aarch64": manifest.ArtifactDigest(arm),
						"linux-x86_64":   manifest.ArtifactDigest(strings.ToUpper(x86)),
					},
				},
				{Name: "metrics", Version: "1.5.0-1", Source: "registry"},
			}},
		},
	}

	out, err := lock.Marshal()
	require.NoError(t, err)

	text := string(out)
	assert.Contains(t, text, "artifacts = {linux-x86_64 = 'sha256:"+x86+"', "+
		"macosx-aarch64 = 'sha256:"+arm+"', rockspec = 'sha256:"+spec+"'}\n")
	assert.Equal(t, 1, strings.Count(text, "artifacts"), text)

	back, err := manifest.ParseLock(out)
	require.NoError(t, err)
	assert.Equal(t, lock.Products, back.Products)

	pins, err := back.Products["default"].Dependencies[0].ArtifactPins()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"linux-x86_64": x86, "macosx-aarch64": arm, "rockspec": spec,
	}, pins)

	pins, err = back.Products["default"].Dependencies[1].ArtifactPins()
	require.NoError(t, err)
	assert.Nil(t, pins, "no artifacts must be no pins, not an empty set")
}

// TestLockArtifactPinsRefuseAMalformedDigest pins that a digest which is not
// sha256:<hex> fails rather than installing the rock unverified.
func TestLockArtifactPinsRefuseAMalformedDigest(t *testing.T) {
	t.Parallel()

	for _, digest := range []string{
		strings.Repeat("a", 64),             // No algorithm.
		"md5:" + strings.Repeat("a", 32),    // Another algorithm.
		"sha256:" + strings.Repeat("a", 63), // Short.
		"sha256:" + strings.Repeat("z", 64), // Not hex.
	} {
		dep := manifest.LockDependency{
			Name: "emmylua", Version: "0.30.0pre1-1", Source: "registry",
			Artifacts: map[string]string{"linux-x86_64": digest},
		}

		_, err := dep.ArtifactPins()

		var invalid *manifest.ValidationError

		require.ErrorAs(t, err, &invalid, digest)
		assert.Equal(t, "artifacts.linux-x86_64", invalid.Field)
		assert.Contains(t, err.Error(), "lock entry emmylua 0.30.0pre1-1")
	}
}

// TestLockIgnoresTheChecksumOfAnOlderTt pins that a lock an older tt wrote,
// with the md5 of the source of a rock under checksum, reads as before and
// loses the key when it is written again: nothing ever checked it.
func TestLockIgnoresTheChecksumOfAnOlderTt(t *testing.T) {
	t.Parallel()

	lock, err := manifest.ParseLock([]byte(`lock_version = '0.1'
manifest_version = '0.1'
generated_by = 'tt 3.0.0-alpha.2'
manifest_hash = 'sha256:abc123'

[lock]
[lock.products]
[lock.products.default]
[[lock.products.default.dependencies]]
name = 'checks'
version = '3.4.1-1'
source = 'registry'
checksum = 'md5:0cc175b9c0f1b6a831c399e269772661'
`))
	require.NoError(t, err)
	assert.Equal(t, []manifest.LockDependency{
		{Name: "checks", Version: "3.4.1-1", Source: "registry"},
	}, lock.Products["default"].Dependencies)

	out, err := lock.Marshal()
	require.NoError(t, err)
	assert.NotContains(t, string(out), "checksum")
}

func TestLockNewerMajorRefused(t *testing.T) {
	t.Parallel()

	_, err := manifest.ParseLock([]byte("lock_version = \"1.0\"\n"))
	require.ErrorIs(t, err, manifest.ErrUnsupportedVersion)
}

func TestLockRequiresVersion(t *testing.T) {
	t.Parallel()

	_, err := manifest.ParseLock([]byte(strings.TrimSpace("generated_by = \"tt 3.1.0\"")))
	require.Error(t, err)
}
