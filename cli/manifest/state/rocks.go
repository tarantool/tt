package state

import (
	"os"
	"path/filepath"

	"github.com/tarantool/go-luarocks/deps"
)

// rocksManifestDir is the tree subdirectory LuaRocks keeps per-rock manifests
// in: <share>/rocks/<name>/<version>/.
const rocksManifestDir = "rocks"

// InstalledVersion reads the version of a rock currently installed in the tree
// from <share>/rocks/<dep>/<version>/. It returns the first version directory
// found; a well-formed tree holds exactly one per rock.
func InstalledVersion(lay Layout, dep string) (string, bool) {
	entries, err := os.ReadDir(filepath.Join(lay.Share, rocksManifestDir, dep))
	if err != nil {
		return "", false
	}

	for _, entry := range entries {
		if entry.IsDir() {
			return entry.Name(), true
		}
	}

	return "", false
}

// RockPaths lists the on-disk paths that hold one version of a rock: its module
// trees under share/ and lib/, and its rock-manifest directory. Install deletes
// these to replace a stale version; uninstall deletes them to drop a dependency
// nobody owns any more.
func RockPaths(lay Layout, dep, version string) []string {
	return []string{
		filepath.Join(lay.Share, dep),
		filepath.Join(lay.Lib, dep),
		filepath.Join(lay.Share, rocksManifestDir, dep, version),
	}
}

// RockRoot is the rock-manifest directory of a rock across all its versions,
// <share>/rocks/<dep>/. Uninstall removes it once the last version is gone, so
// no empty husk is left behind to make the rock look installed.
func RockRoot(lay Layout, dep string) string {
	return filepath.Join(lay.Share, rocksManifestDir, dep)
}

// SameVersion reports whether two version strings denote the same version,
// tolerating formatting differences (a trailing "-1" revision, say).
func SameVersion(left, right string) bool {
	if left == right {
		return true
	}

	parsedLeft, err := deps.ParseVersion(left)
	if err != nil {
		return false
	}

	parsedRight, err := deps.ParseVersion(right)
	if err != nil {
		return false
	}

	return deps.Compare(parsedLeft, parsedRight) == 0
}
