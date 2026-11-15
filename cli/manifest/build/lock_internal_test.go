package build

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/projectlock"
)

// busyManifest is my-app with a component whose build fails when another
// build of the project runs at the same time: it marks the project busy,
// waits and clears the mark, and a mark already there fails it.
const busyManifest = manifestHead + `
[components.busy]
path = 'busy'

[components.busy.build]
backend = 'shell'
command = 'sh'
args = ['-c', 'mkdir busy.mark && sleep 0.3 && rmdir busy.mark']
cwd = '.'

[hooks.post_build]
command = 'sh'
args = ['-c', 'test -n "$TT_PROJECT_LOCK"']

[products.default]
components = ['lua', 'busy']
default = true
`

// TestRun_concurrentBuilds runs builds of one project at once: each holds
// the lock of the project for the whole of it, so they run one after
// another and all of them succeed, and the hooks of each run under its
// lock, handed down to them in TT_PROJECT_LOCK.
func TestRun_concurrentBuilds(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, busyManifest, map[string]string{"busy/init.lua": "return {}\n"})

	const builds = 3

	errs := make(chan error, builds)

	for range builds {
		go func() { errs <- Run(context.Background(), dryOptions(dir)) }()
	}

	for range builds {
		require.NoError(t, <-errs)
	}

	assert.NoDirExists(t, filepath.Join(dir, "busy.mark"))
}

// TestRun_cancelledWait stops a build waiting for the lock when its context
// is done.
func TestRun_cancelledWait(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, busyManifest, map[string]string{"busy/init.lua": "return {}\n"})

	_, release, err := projectlock.Acquire(context.Background(), dir)
	require.NoError(t, err)

	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = Run(ctx, dryOptions(dir))
	require.ErrorIs(t, err, context.Canceled)
	assert.NoFileExists(t, filepath.Join(dir, lockFileName), "nothing was done")
}

// TestRun_underTheLock builds with the context of a lock held: the build
// works under it rather than wait for it.
func TestRun_underTheLock(t *testing.T) {
	t.Parallel()

	dir := setupProject(t, busyManifest, map[string]string{"busy/init.lua": "return {}\n"})

	ctx, release, err := projectlock.Acquire(context.Background(), dir)
	require.NoError(t, err)

	defer release()

	require.NoError(t, Run(ctx, dryOptions(dir)))
	require.NoError(t, Run(ctx, dryOptions(dir)))
}
