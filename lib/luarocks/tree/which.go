package tree

import (
	"os"
	"path/filepath"
	"strings"
)

// Which resolves a dotted Lua module name to the on-disk file that would
// satisfy `require("<module>")` against this tree.
//
// Search order matches upstream `package.path` then `package.cpath`:
//
//  1. DeployLuaDir/<slashed>.lua
//  2. DeployLuaDir/<slashed>/init.lua
//  3. DeployLibDir/<slashed>.so
//
// Returns ("", false) when none of the above exist on disk.
//
// Conflict-munged siblings (`.../foo.lua~name_version`) are NOT considered
// — only the active (plain) path. That mirrors how Tarantool's `require`
// works against the rocks tree.
func (t *Tree) Which(module string) (string, bool) {
	slashed := strings.ReplaceAll(module, ".", string(filepath.Separator))

	candidates := []string{
		filepath.Join(t.DeployLuaDir(), slashed+".lua"),
		filepath.Join(t.DeployLuaDir(), slashed, "init.lua"),
		filepath.Join(t.DeployLibDir(), slashed+".so"),
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, true
		}
	}

	return "", false
}
