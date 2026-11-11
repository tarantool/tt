package resolve_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/resolve"
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

	// metrics comes from a server with a tt-rocks-index.json, which lists the
	// digest of every file; checks from a classic one, which lists none.
	fake := newFakeAdapter().
		add("metrics", "1.5.0-1", "d41d8cd98f00b204e9800998ecf8427e", dep(t, "checks", ">=3.0")).
		withArtifacts("metrics", "1.5.0-1",
			"linux-x86_64", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			"macosx-aarch64", "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae",
			"rockspec", "fcde2b2edba56bf408601fb721fe9b5c338d10ee429ea04fae5511b68fbf8fb9").
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

	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err)

	assert.Equal(t, string(want), string(got))
}
