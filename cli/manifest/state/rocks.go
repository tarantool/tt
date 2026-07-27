package state

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/tarantool/go-luarocks/deps"
	"github.com/tarantool/go-luarocks/manif"
)

// rocksManifestDir is the tree subdirectory LuaRocks keeps per-rock manifests
// in: <share>/rocks/<name>/<version>/.
const rocksManifestDir = "rocks"

// DependencyEdges reads which rock requires which, from the tree manifest
// LuaRocks maintains at <share>/rocks/manifest. Its `dependencies` table records
// every installed rock's own resolved requirements, so the edges a lock throws
// away — it stores the closure flattened, with no parent — are recoverable from
// the tree itself.
//
// The result maps a rock to the rocks it pulls in, deduplicated across versions
// and in name order. A tree with no manifest, or one that cannot be parsed,
// yields nil rather than an error: the edges are an enrichment, and a tree
// assembled by an offline extraction may not carry them. Callers fall back to
// reporting indirect rocks without a parent.
func DependencyEdges(lay Layout) map[string][]string {
	tree, err := manif.ReadTreeManifest(filepath.Join(lay.Share, rocksManifestDir))
	if err != nil {
		return nil
	}

	edges := make(map[string][]string, len(tree.Dependencies))

	for rock, versions := range tree.Dependencies {
		required := map[string]struct{}{}

		for _, deps := range versions {
			for _, dep := range deps {
				// A rock never counts as its own requirement; a self-edge would make
				// the graph walk cycle on the first step.
				if dep.Name != rock {
					required[dep.Name] = struct{}{}
				}
			}
		}

		if len(required) == 0 {
			continue
		}

		names := make([]string, 0, len(required))
		for name := range required {
			names = append(names, name)
		}

		sort.Strings(names)

		edges[rock] = names
	}

	return edges
}

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
