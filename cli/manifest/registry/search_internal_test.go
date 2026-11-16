package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSortMatches pins the order of tt package search: the rock searched for
// first, then the others by name; within a rock, the releases newest first
// and scm after them; one version of two servers in the order of the
// servers.
func TestSortMatches(t *testing.T) {
	t.Parallel()

	const (
		tarantool = "https://rocks.tarantool.org"
		luarocks  = "https://luarocks.org"
	)

	matches := []Match{
		{Name: "checks", Version: "scm-1", Server: tarantool},
		{Name: "checks", Version: "1.0.0-1", Server: tarantool},
		{Name: "checks", Version: "3.4.1-1", Server: tarantool},
		{Name: "luatypechecks", Version: "1.3.4-1", Server: luarocks},
		{Name: "checks", Version: "1.0-3", Server: luarocks},
		{Name: "checks", Version: "3.10.0-1", Server: tarantool},
		{Name: "tarantool-checks", Version: "3.1.0-1", Server: luarocks},
		{Name: "checks", Version: "3.4.1-1", Server: luarocks},
		{Name: "achecks", Version: "1.0.0-1", Server: luarocks},
	}

	sortMatches(matches, "checks")

	assert.Equal(t, []Match{
		{Name: "checks", Version: "3.10.0-1", Server: tarantool},
		{Name: "checks", Version: "3.4.1-1", Server: tarantool},
		{Name: "checks", Version: "3.4.1-1", Server: luarocks},
		// 1.0 is 1.0.0 to LuaRocks, and the revision tells them apart.
		{Name: "checks", Version: "1.0-3", Server: luarocks},
		{Name: "checks", Version: "1.0.0-1", Server: tarantool},
		{Name: "checks", Version: "scm-1", Server: tarantool},
		{Name: "achecks", Version: "1.0.0-1", Server: luarocks},
		{Name: "luatypechecks", Version: "1.3.4-1", Server: luarocks},
		{Name: "tarantool-checks", Version: "3.1.0-1", Server: luarocks},
	}, matches)
}
