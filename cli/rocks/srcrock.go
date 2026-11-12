package rocks

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/remote"
	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

// ErrEmptySrcRock reports a rock LuaRocks installed from the source rock of a
// tt package: its rockspec has nothing to build, so the rock holds no module.
var ErrEmptySrcRock = errors.New("LuaRocks built nothing of the source rock of a tt package")

// treeFlag is the LuaRocks option naming the tree a command works on.
const treeFlag = "--tree"

// rockTree is the tree the LuaRocks command line args works on: the one
// --tree names, relative to workingDir, or workingDir/.rocks.
func rockTree(workingDir string, args []string) string {
	root := filepath.Join(workingDir, ".rocks")

	for idx, arg := range args {
		value, found := strings.CutPrefix(arg, treeFlag+"=")

		switch {
		case found:
		case arg == treeFlag && idx+1 < len(args):
			value = args[idx+1]
		default:
			continue
		}

		if filepath.IsAbs(value) {
			root = value
		} else {
			root = filepath.Join(workingDir, value)
		}
	}

	return root
}

// treeBefore is the tree at root before a LuaRocks command, as checkSrcRocks
// compares the tree after it with.
type treeBefore struct {
	// rocks are the rock versions the tree held.
	rocks map[tree.RockRef]bool
	// absent says there was nothing at root: the command made the tree.
	absent bool
}

// inspectBefore reads the tree at root before a LuaRocks command. A tree that
// cannot be read holds no rock, as far as checkSrcRocks is concerned.
func inspectBefore(ctx context.Context, root string) treeBefore {
	_, err := os.Lstat(root)
	before := treeBefore{rocks: map[tree.RockRef]bool{}, absent: errors.Is(err, fs.ErrNotExist)}

	view, err := tree.Inspect(ctx, root)
	if err != nil {
		return before
	}

	for _, ref := range view.Rocks() {
		before.rocks[ref] = true
	}

	return before
}

// checkSrcRocks finds the rocks a LuaRocks command installed into the tree at
// root, those it holds now and not before, that are source rocks of tt
// packages with nothing built (emptySrcRock), removes them and fails with
// ErrEmptySrcRock naming them. LuaRocks installs such a rock as any other: it
// unpacks it and runs its build, which is done, its build type none, since the
// rockspec is that of the binary rocks; only tt builds the package it holds.
// Left in the tree, the rock would satisfy the dependencies on it with no
// module to require.
//
// A tree the command made, which holds no rock once they are removed, is
// removed as a whole: its manifest and its lock file are all LuaRocks left of
// the command there. LuaRocks has printed that the rock is installed by then,
// on the terminal tt does not stand between it and; the error says it is not.
func checkSrcRocks(ctx context.Context, root string, before treeBefore) error {
	view, err := tree.Inspect(ctx, root)
	if err != nil {
		return nil //nolint:nilerr // LuaRocks itself reports a tree it broke.
	}

	var empty []tree.RockRef

	for _, ref := range view.Rocks() {
		if !before.rocks[ref] && emptySrcRock(view, ref) {
			empty = append(empty, ref)
		}
	}

	if len(empty) == 0 {
		return nil
	}

	rocksTree, err := tree.Open(luarocks.Config{Tree: root})
	if err == nil {
		err = rocksTree.Update(ctx, func(change *tree.Tx) error {
			for _, ref := range empty {
				removeErr := change.Remove(ref)
				if removeErr != nil {
					return fmt.Errorf("removing %s %s: %w", ref.Name, ref.Version, removeErr)
				}
			}

			return nil
		})
	}

	if err != nil {
		return fmt.Errorf("%w: %s, which LuaRocks printed is installed with no module, "+
			"tt could not remove from the tree: %w", ErrEmptySrcRock, refList(empty), err)
	}

	if before.absent {
		removeEmptyTree(ctx, root)
	}

	verb, pronoun := "is", "it"
	if len(empty) > 1 {
		verb, pronoun = "are", "them"
	}

	return fmt.Errorf("%w: %s %s not installed, whatever LuaRocks printed above; tt "+
		"removed %s from the tree again. Install the binary rock of %s from a server "+
		"that has one, or depend on the rock in app.manifest.toml and run tt package "+
		"build, which builds the source rock with its manifest", ErrEmptySrcRock,
		refList(empty), verb, pronoun, remote.HostArch())
}

// removeEmptyTree removes the tree at root when it holds no rock. A tree that
// cannot be read or cannot be removed is left as it is: the error of the
// command says what matters, that the rock is not installed.
func removeEmptyTree(ctx context.Context, root string) {
	view, err := tree.Inspect(ctx, root)
	if err != nil || len(view.Rocks()) > 0 {
		return
	}

	_ = os.RemoveAll(root)
}

// emptySrcRock reports whether the installed rock ref is the rock of a tt
// package (rocktree.IsReleaseRockspec) with no module and no command: a binary
// rock of a package holds its version.lua at least, so such a rock is one
// LuaRocks built from the source rock.
func emptySrcRock(view *tree.View, ref tree.RockRef) bool {
	files, err := view.Files(ref)
	if err != nil {
		return false
	}

	release := false

	for _, file := range files {
		switch file.Section {
		case tree.SectionLua, tree.SectionLib, tree.SectionBin:
			return false
		case tree.SectionRockspec:
			src, readErr := os.ReadFile(file.Path)

			release = readErr == nil && rocktree.IsReleaseRockspec(src)
		case tree.SectionConf, tree.SectionDoc, tree.SectionOther:
		}
	}

	return release
}

// refList names the rock versions refs: "a 1.0.0-1, b 2.0.0-1".
func refList(refs []tree.RockRef) string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name+" "+ref.Version)
	}

	return strings.Join(names, ", ")
}
