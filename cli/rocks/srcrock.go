package rocks

import (
	"context"
	"errors"
	"fmt"
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
var ErrEmptySrcRock = errors.New("LuaRocks installed the source rock of a tt package " +
	"with nothing built")

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

// installedRocks lists the rock versions the tree at root holds. A tree that
// cannot be read holds none, as far as checkSrcRocks is concerned.
func installedRocks(ctx context.Context, root string) map[tree.RockRef]bool {
	view, err := tree.Inspect(ctx, root)
	if err != nil {
		return map[tree.RockRef]bool{}
	}

	refs := view.Rocks()

	out := make(map[tree.RockRef]bool, len(refs))
	for _, ref := range refs {
		out[ref] = true
	}

	return out
}

// checkSrcRocks finds the rocks a LuaRocks command installed into the tree at
// root, those it holds now and not before, that are source rocks of tt
// packages with nothing built (emptySrcRock), removes them and fails with
// ErrEmptySrcRock naming them. LuaRocks installs such a rock as any other: it
// unpacks it and runs its build, which is done, its build type none, since the
// rockspec is that of the binary rocks; only tt builds the package it holds.
// Left in the tree, the rock would satisfy the dependencies on it with no
// module to require.
func checkSrcRocks(ctx context.Context, root string, before map[tree.RockRef]bool) error {
	view, err := tree.Inspect(ctx, root)
	if err != nil {
		return nil //nolint:nilerr // LuaRocks itself reports a tree it broke.
	}

	var empty []tree.RockRef

	for _, ref := range view.Rocks() {
		if !before[ref] && emptySrcRock(view, ref) {
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
		return fmt.Errorf("%w: %s, which tt could not remove: %w", ErrEmptySrcRock,
			refList(empty), err)
	}

	return fmt.Errorf("%w: %s; tt removed it from the tree again. Install the binary "+
		"rock of %s from a server that has one, or depend on the rock in "+
		"app.manifest.toml and run tt package build, which builds the source rock "+
		"with its manifest", ErrEmptySrcRock, refList(empty), remote.HostArch())
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
