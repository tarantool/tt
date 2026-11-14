package build

import (
	"path/filepath"
)

// Hook names, matching the manifest's [hooks.<name>] tables. The hooks run
// through the backends that run hooks (runHook).
const (
	hookPreBuild  = "pre_build"
	hookPostBuild = "post_build"
)

// resolveDir resolves a manifest-supplied directory against the project root: an
// empty value defaults to the root, an absolute value is used verbatim, and a
// relative value is joined onto the root. The result is always absolute (the
// backend contract requires it) as long as projectDir is.
func resolveDir(projectDir, dir string) string {
	switch {
	case dir == "":
		return projectDir
	case filepath.IsAbs(dir):
		return dir
	default:
		return filepath.Join(projectDir, dir)
	}
}
