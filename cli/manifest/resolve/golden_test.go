package resolve_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/resolve"
)

// goldenManifest is a fixed manifest whose resolution the golden lock pins
// byte-for-byte. Its bytes also determine manifest_hash, so it must not drift.
const goldenManifest = `manifest_version = '0.1'
[package]
name = 'my-app'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[dependencies]
metrics = '>=1.0.0'
[components.app]
path = '.'
[products.default]
components = ['app']
default = true
`

// TestGoldenLock resolves a fixed manifest against a fixed fake registry and
// asserts the marshaled lock reproduces testdata/my-app.lock byte-for-byte. Run
// with UPDATE_GOLDEN=1 to regenerate the golden after an intended format change.
func TestGoldenLock(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("metrics", "1.5.0-1", "d41d8cd98f00b204e9800998ecf8427e", dep(t, "checks", ">=3.0")).
		add("checks", "3.1.0-1", "0cc175b9c0f1b6a831c399e269772661")

	man := parseManifest(t, goldenManifest)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	got, err := lock.Marshal()
	require.NoError(t, err)

	goldenPath := filepath.Join("testdata", "my-app.lock")

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.WriteFile(goldenPath, got, 0o600))
	}

	want, err := os.ReadFile(goldenPath) //nolint:gosec // fixed testdata path
	require.NoError(t, err)

	assert.Equal(t, string(want), string(got))
}
