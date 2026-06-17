package manifest_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
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
					Checksum:    "md5:deadbeef",
					Path:        "",
					ContentHash: "",
				},
				{
					Name:        "local-helper",
					Version:     "0.1.0",
					Source:      "path",
					Checksum:    "",
					Path:        "../helper",
					ContentHash: "sha256:cafe",
				},
			}},
			"minimal": {Dependencies: []manifest.LockDependency{
				{
					Name:        "luasocket",
					Version:     "3.0.4",
					Source:      "registry",
					Checksum:    "md5:deadbeef",
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
