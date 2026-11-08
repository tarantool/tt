package state

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/tarantool/go-luarocks/deps"
	"github.com/tarantool/go-luarocks/tree"
)

// rocksManifestDir is the tree subdirectory LuaRocks keeps per-rock manifests
// in: <share>/rocks/<name>/<version>/.
const rocksManifestDir = "rocks"

// InspectTree reads the rocks tree of lay as go-luarocks keeps it: the tree
// manifest and the rock_manifest of every rock it lists. A tree that does not
// exist, or has no manifest yet, is an empty one; nothing is created.
//
// The View is what tt asks about the rocks installed - which versions there
// are, which rock requires which, where a command's script is - so that the
// answer is the one the tree manifest gives, rather than a guess from the
// directories under share/tarantool/rocks/.
func InspectTree(ctx context.Context, lay Layout) (*tree.View, error) {
	view, err := tree.Inspect(ctx, lay.Tree)
	if err != nil {
		return nil, fmt.Errorf("reading the rocks tree %s: %w", lay.Tree, err)
	}

	return view, nil
}

// InstalledVersion returns the newest version of the rock dep the tree view
// lists. A well-formed tree tt manages holds one version per rock.
func InstalledVersion(view *tree.View, dep string) (string, bool) {
	if view == nil {
		return "", false
	}

	versions := view.Versions(dep)
	if len(versions) == 0 {
		return "", false
	}

	return versions[0], true
}

// DependencyEdges reads which rock requires which from the tree view: the
// dependencies each installed rock declares, as the tree manifest records
// them. They recover the edges a lock throws away - it stores the closure
// flattened, with no parent.
//
// The result maps a rock to the rocks it pulls in, deduplicated across versions
// and in name order. A nil view yields nil: the edges are an enrichment, and
// callers fall back to reporting indirect rocks without a parent.
func DependencyEdges(view *tree.View) map[string][]string {
	if view == nil {
		return nil
	}

	required := map[string]map[string]struct{}{}

	for _, ref := range view.Rocks() {
		for _, dep := range view.Dependencies(ref) {
			// A rock never counts as its own requirement; a self-edge would make
			// the graph walk cycle on the first step.
			if dep.Name == ref.Name {
				continue
			}

			if required[ref.Name] == nil {
				required[ref.Name] = map[string]struct{}{}
			}

			required[ref.Name][dep.Name] = struct{}{}
		}
	}

	edges := make(map[string][]string, len(required))

	for rock, set := range required {
		names := make([]string, 0, len(set))
		for name := range set {
			names = append(names, name)
		}

		sort.Strings(names)

		edges[rock] = names
	}

	return edges
}

// RockPaths lists the on-disk paths that hold one version of a rock as a tt
// that changed the tree behind its manifest's back laid them out: its module
// trees under share/ and lib/, named after it, and its rock dir. Uninstall and
// install delete these for a rock the tree manifest does not list, which is
// what such a tt left: the files of a package laid out by hand, and the rocks
// it extracted from an archive.
func RockPaths(lay Layout, dep, version string) []string {
	return []string{
		filepath.Join(lay.Share, dep),
		filepath.Join(lay.Lib, dep),
		filepath.Join(lay.Share, rocksManifestDir, dep, version),
	}
}

// RockRoot is the rocks-dir directory of a rock across all its versions,
// <share>/rocks/<dep>/. Once RockPaths of the last version are gone it is
// removed too, so no empty husk is left behind to make the rock look
// installed.
func RockRoot(lay Layout, dep string) string {
	return filepath.Join(lay.Share, rocksManifestDir, dep)
}

// RemoveUntracked deletes RockPaths of a version of the rock dep the tree
// manifest does not list, and RockRoot when nothing is left in it.
func RemoveUntracked(lay Layout, dep, version string) error {
	for _, path := range RockPaths(lay, dep, version) {
		err := os.RemoveAll(path)
		if err != nil {
			return fmt.Errorf("removing %s %s: %w", dep, version, err)
		}
	}

	// Remove() rather than RemoveAll(): a root still holding another version
	// directory must survive, and the ENOTEMPTY that produces is the signal to
	// leave it alone. Either way the failure is cosmetic - an empty directory,
	// not a broken tree - so it is not returned.
	_ = os.Remove(RockRoot(lay, dep))

	return nil
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
