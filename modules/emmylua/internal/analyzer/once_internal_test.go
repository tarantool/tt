package analyzer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFirst checks that First reports the first call with a key alone, a
// key of its own parts, and every call with no cache or one it cannot
// write.
func TestFirst(t *testing.T) {
	t.Parallel()

	cache := t.TempDir()

	assert.True(t, First(cache, "no-stubs", "/usr/bin/tarantool"))
	assert.False(t, First(cache, "no-stubs", "/usr/bin/tarantool"))
	assert.True(t, First(cache, "no-stubs", "/opt/bin/tarantool"))
	// The parts are kept apart: "a", "bc" is not "ab", "c".
	assert.True(t, First(cache, "a", "bc"))
	assert.True(t, First(cache, "ab", "c"))

	assert.True(t, First("", "key"))
	assert.True(t, First("", "key"))

	// A cache that is a file: nothing is remembered, nothing is lost.
	file := filepath.Join(t.TempDir(), "cache")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	assert.True(t, First(file, "key"))
	assert.True(t, First(file, "key"))
}
