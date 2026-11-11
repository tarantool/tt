package resolve_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/resolve"
)

// TestTarantoolIsNoRock: a dependency of a rock on tarantool is checked
// against [platform].tarantool rather than resolved as a rock: one its lower
// bound meets resolves quietly and locks nothing for tarantool.
func TestTarantoolIsNoRock(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("crud", "1.5.0-1", "aaa", dep(t, "tarantool", ">= 2.10"), dep(t, "lua", ">= 5.1"))

	man := parseManifest(t, oneProduct+`[dependencies]
crud = '>=1.0.0'
`)

	engine := resolve.NewEngine(fake, "", "tt 3.4.0")

	lock, warnings, err := engine.Resolve(context.Background(), man)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	deps := lock.Products["default"].Dependencies
	require.Len(t, deps, 1)
	assert.Equal(t, "crud", deps[0].Name)
	assert.Zero(t, fake.resolves["tarantool"])
}

// TestTarantoolAboveTheLowerBound: a rock that needs a newer Tarantool than
// the lowest one the project runs on resolves, with a warning saying to raise
// the bound, once for every product that has it.
func TestTarantoolAboveTheLowerBound(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("wasm", "0.1.0-1", "aaa", dep(t, "tarantool", ">= 3.6"))

	man := parseManifest(t, oneProduct+`[products.other]
components = ['app']

[dependencies]
wasm = '0.1.0'
`)

	_, warnings, err := resolve.NewEngine(fake, "", "tt 3.4.0").Resolve(context.Background(), man)
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "wasm requires tarantool >=3.6")
	assert.Contains(t, warnings[0], `[platform].tarantool ">=3.0.0" allows 3.0.0 too`)
}

// TestTarantoolExcluded: a rock that runs on no Tarantool the project runs on
// fails the resolution.
func TestTarantoolExcluded(t *testing.T) {
	t.Parallel()

	fake := newFakeAdapter().
		add("old", "1.0.0-1", "aaa", dep(t, "tarantool", "< 2.0"))

	man := parseManifest(t, oneProduct+`[dependencies]
old = '1.0.0'
`)

	_, _, err := resolve.NewEngine(fake, "", "tt 3.4.0").Resolve(context.Background(), man)
	require.ErrorIs(t, err, resolve.ErrTarantoolExcluded)
	assert.Contains(t, err.Error(), "old requires tarantool <2.0")
}
