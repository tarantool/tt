package resolve_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/resolve"
)

func TestProductsGetDistinctClosures(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("xx", "1.0.0-1", "x").
		add("yy", "1.0.0-1", "y")

	man := parseManifest(t, `manifest_version = '0.1'
[package]
name = 'app'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[components.c1]
path = 'a'
[components.c1.dependencies]
xx = '>=1.0.0'
[components.c2]
path = 'b'
[components.c2.dependencies]
yy = '>=1.0.0'
[products.p1]
components = ['c1']
default = true
[products.p2]
components = ['c2']
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	assert.Equal(t, []string{"xx"}, depNames(lock.Products["p1"].Dependencies))
	assert.Equal(t, []string{"yy"}, depNames(lock.Products["p2"].Dependencies))
}

func TestComponentDependencyMergesWithGlobal(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("metrics", "1.5.0-1", "b").
		add("metrics", "2.0.0-1", "c")

	man := parseManifest(t, `manifest_version = '0.1'
[package]
name = 'app'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[dependencies]
metrics = '>=1.0.0'
[components.app]
path = '.'
[components.app.dependencies]
metrics = '<2.0.0'
[products.default]
components = ['app']
default = true
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, _, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)

	// Global >=1.0.0 AND component <2.0.0 => the newest in [1.0.0, 2.0.0).
	got := findDep(t, lock.Products["default"].Dependencies, "metrics")
	assert.Equal(t, "1.5.0-1", got.Version)
}

func TestGlobalComponentConflictErrors(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("metrics", "1.0.0-1", "a").
		add("metrics", "2.0.0-1", "b")

	man := parseManifest(t, `manifest_version = '0.1'
[package]
name = 'app'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[dependencies]
metrics = '==1.0.0-1'
[components.app]
path = '.'
[components.app.dependencies]
metrics = '==2.0.0-1'
[products.default]
components = ['app']
default = true
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	_, _, err := engine.Resolve(context.Background(), man)
	require.ErrorIs(t, err, resolve.ErrConflict)
	assert.Contains(t, err.Error(), "metrics")
}
