package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lockWith writes a lock into a fresh project directory and returns it.
func lockWith(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, lockFileName), []byte(body), 0o600))

	return dir
}

// lockHeader is the preamble every fixture lock needs to parse.
const lockHeader = `lock_version = '0.1'
manifest_version = '0.1'
generated_by = 'tt 3.0.0'
manifest_hash = 'sha256:0000000000000000000000000000000000000000000000000000000000000000'
`

func TestLockedRefsWalksEveryClosure(t *testing.T) {
	t.Parallel()

	dir := lockWith(t, lockHeader+`
[[lock.products.server.dependencies]]
name = 'metrics'
version = '1.0.0-1'
source = 'registry'

[[lock.products.tool.dependencies]]
name = 'checks'
version = '3.1.0-1'
source = 'registry'

[[lock.dev_dependencies]]
name = 'luatest'
version = '1.0.1-1'
source = 'registry'
`)

	refs, err := lockedRefs(DownloadOptions{ProjectDir: dir})
	require.NoError(t, err)

	// Every product's closure plus the dev closure, in name order.
	assert.Equal(t, []Ref{
		{Name: "checks", Version: "3.1.0-1"},
		{Name: "luatest", Version: "1.0.1-1"},
		{Name: "metrics", Version: "1.0.0-1"},
	}, refs)
}

func TestLockedRefsDeduplicatesSharedRocks(t *testing.T) {
	t.Parallel()

	dir := lockWith(t, lockHeader+`
[[lock.products.server.dependencies]]
name = 'checks'
version = '3.1.0-1'
source = 'registry'

[[lock.products.tool.dependencies]]
name = 'checks'
version = '3.1.0-1'
source = 'registry'

[[lock.dev_dependencies]]
name = 'checks'
version = '3.1.0-1'
source = 'registry'
`)

	refs, err := lockedRefs(DownloadOptions{ProjectDir: dir})
	require.NoError(t, err)

	// Products share most of a closure; the same file must not be fetched
	// once per mention.
	assert.Equal(t, []Ref{{Name: "checks", Version: "3.1.0-1"}}, refs)
}

func TestLockedRefsKeepsTwoVersionsApart(t *testing.T) {
	t.Parallel()

	dir := lockWith(t, lockHeader+`
[[lock.products.server.dependencies]]
name = 'checks'
version = '3.1.0-1'
source = 'registry'

[[lock.products.tool.dependencies]]
name = 'checks'
version = '3.2.0-1'
source = 'registry'
`)

	refs, err := lockedRefs(DownloadOptions{ProjectDir: dir})
	require.NoError(t, err)

	// Two products may pin the same rock differently; a mirror has to carry
	// both, or one of them stops building.
	assert.Equal(t, []Ref{
		{Name: "checks", Version: "3.1.0-1"},
		{Name: "checks", Version: "3.2.0-1"},
	}, refs)
}

func TestLockedRefsSkipsNonRegistrySources(t *testing.T) {
	t.Parallel()

	dir := lockWith(t, lockHeader+`
[[lock.products.server.dependencies]]
name = 'metrics'
version = '1.0.0-1'
source = 'registry'

[[lock.products.server.dependencies]]
name = 'local-lib'
version = '0.1.0-1'
source = 'path'
path = 'libs/local-lib'
`)

	var warnings []string

	refs, err := lockedRefs(DownloadOptions{
		ProjectDir: dir,
		Warn:       func(msg string) { warnings = append(warnings, msg) },
	})
	require.NoError(t, err)

	assert.Equal(t, []Ref{{Name: "metrics", Version: "1.0.0-1"}}, refs)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "local-lib")
}

func TestLockedRefsWithoutALock(t *testing.T) {
	t.Parallel()

	_, err := lockedRefs(DownloadOptions{ProjectDir: t.TempDir()})
	require.ErrorIs(t, err, ErrNoLock)
	// The message has to name the command that produces a lock; a bare "no
	// lock file" leaves the reader to guess.
	assert.Contains(t, err.Error(), "tt package resolve")
}
