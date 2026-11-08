// Package rocktree holds what tt does to a rocks tree beyond installing the
// rocks of a lock, through the tree package of go-luarocks, which keeps the
// files of a tree and its manifest together:
//
//   - the package's own files - the components a build lays out, version.lua
//     and the programs of its executable components - are a rock of the tree
//     like any dependency, installed from a stage laid out as an unpacked
//     binary rock (Rockspec, InstallPackage), so that the tree manifest owns
//     every file tt puts there, and pack, install and uninstall move the
//     package by its rock;
//   - a tree an older tt damaged by changing it behind the manifest's back -
//     rock dirs removed without their entries, rocks extracted without any -
//     is repaired once by rebuilding its manifest from the rocks on disk
//     (Repair);
//   - the files of a package an older tt laid out by hand, which no rock
//     owns, are taken over by the package's rock (StageUntracked,
//     InstallPackage).
package rocktree

import (
	"context"
	"fmt"

	"github.com/tarantool/go-luarocks/tree"
)

// Repair rebuilds the manifest of the tree rocksTree from the rocks on disk when
// Check finds it out of step with them: an entry without its rock dir, which
// an older tt left removing a rock's files by hand, a rock dir the manifest
// does not list, which it left extracting a package's rocks, or a manifest
// that does not parse. log is told that the tree is repaired, and of every
// rock dir the rebuilt manifest leaves out. A tree in step is left alone.
func Repair(ctx context.Context, rocksTree *tree.Tree, log func(string)) error {
	report, err := rocksTree.Check(ctx, tree.CheckOpts{BinWrap: nil})
	if err != nil {
		return fmt.Errorf("checking the rocks tree %s: %w", rocksTree.Tree, err)
	}

	stale := 0

	for _, issue := range report.Issues {
		switch issue.Kind {
		case tree.IssueStaleEntry, tree.IssueOrphanRock, tree.IssueManifestUnreadable:
			stale++
		default:
		}
	}

	if stale == 0 {
		return nil
	}

	if log != nil {
		log(fmt.Sprintf("the manifest of %s is out of step with its rocks (%d problems); "+
			"rebuilding it from the rocks on disk", rocksTree.Tree, stale))
	}

	rebuilt, err := rocksTree.Rebuild(ctx, tree.RebuildOpts{Strict: false})
	if err != nil {
		return fmt.Errorf("rebuilding the manifest of %s: %w", rocksTree.Tree, err)
	}

	if log != nil {
		for _, skipped := range rebuilt.Skipped {
			log(fmt.Sprintf("%s is left out of the rebuilt manifest: %s", skipped.Dir, skipped.Err))
		}
	}

	return nil
}
