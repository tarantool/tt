package install

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// openTree opens the rocks tree of lay with the command wrappers the native
// backend writes for the Tarantool of opts, or the tarantool executable when
// it is set, so that a rock the install puts there through the tree gets the
// same ones as a rock it fetches.
func openTree(opts Options, lay state.Layout, executable string) (*tree.Tree, error) {
	info := opts.Tarantool
	if executable != "" && executable != info.Executable {
		// The wrappers only name the interpreter; nothing is built here.
		info = rocks.TarantoolInfo{Executable: executable, Prefix: "", Version: ""}
	}

	cfg := rocks.BuildConfig(info, rocks.ConfigOptions{
		Tree:       lay.Tree,
		WorkingDir: lay.Root,
		Servers:    opts.Servers,
		Logger:     opts.Logger,
	})

	rocksTree, err := tree.Open(cfg)
	if err != nil {
		return nil, fmt.Errorf("opening the rocks tree %s: %w", lay.Tree, err)
	}

	rocksTree.BinWrap = client.BinWrapFor(cfg)

	return rocksTree, nil
}

// repairTree rebuilds the manifest of the project's rocks tree from the rocks
// on disk when it is out of step with them (rocktree.Repair), before the
// install plans against it: a tt that extracted the rocks of an archive
// without recording them, or removed rocks without their entries, left a
// manifest that does not say what is installed.
func repairTree(ctx context.Context, opts Options, lay state.Layout) error {
	_, err := os.Stat(lay.Tree)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	rocksTree, err := openTree(opts, lay, "")
	if err != nil {
		return err
	}

	err = rocktree.Repair(ctx, rocksTree, opts.Warn)
	if err != nil {
		return fmt.Errorf("repairing the rocks tree: %w", err)
	}

	return nil
}

// wrapperExecutable is the tarantool the command wrappers of the rocks the
// install puts into a tree run: the one of opts, which tt found for the
// project, else the one the archive bundles, which the install has just put
// under root, else none, the native backend's default.
func wrapperExecutable(opts Options, root string, runtimes []bundledRuntime) string {
	if opts.Tarantool.Executable != "" {
		return opts.Tarantool.Executable
	}

	return bundledTarantool(root, runtimes)
}

// bundledTarantool is the executable of the tarantool among runtimes, the
// runtimes an archive bundles installed under root; empty without one.
func bundledTarantool(root string, runtimes []bundledRuntime) string {
	for _, runtime := range runtimes {
		if runtime.program.IsTarantool() {
			return runtime.executable(root)
		}
	}

	return ""
}

// versionExecutable is the tarantool the command wrappers of an installed
// version run: the one the archive bundles, which the version runs on, else
// the one of opts.
func versionExecutable(opts Options, root string, runtimes []bundledRuntime) string {
	if executable := bundledTarantool(root, runtimes); executable != "" {
		return executable
	}

	return opts.Tarantool.Executable
}

// rewrapVersion writes the command wrappers of the rocks of the version
// installed at lay for its root and for the tarantool executable, once it
// is in place: the archive carries a rock's command as its script, or, packed
// by an older tt, as a wrapper naming the tree and the interpreter of the
// machine it was packed on, and LuaRocks' wrapper names the tree's root by
// path. The tree manifest is rebuilt first when it is out of step with the
// rocks, as an older tt left it in the archive. A wrapper that cannot be
// written is a warning: the command is the one the archive carried.
func rewrapVersion(ctx context.Context, opts Options, lay state.Layout, executable string) error {
	_, err := os.Stat(lay.Tree)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	rocksTree, err := openTree(opts, lay, executable)
	if err != nil {
		return err
	}

	err = rocktree.Repair(ctx, rocksTree, opts.Warn)
	if err != nil {
		return fmt.Errorf("repairing the rocks tree: %w", err)
	}

	err = rocksTree.Update(ctx, func(change *tree.Tx) error {
		_, err := change.Rewrap()

		return err //nolint:wrapcheck // Reported below.
	})
	if err != nil {
		// The wrappers rewritten before the failure stay so; the others are
		// those the archive carried, which an older tt installed as they were.
		opts.warn(fmt.Sprintf("cannot write the command wrappers of %s: %s", lay.Tree, err))
	}

	return nil
}

// alignDirModes gives every directory of the tree at root the mode of the
// directory like, as tt makes the directories of an installation: the rocks
// tt fetches or grafts into a version get theirs from go-luarocks, which makes
// them 0750 whatever the umask, and in the system scope the instances of the
// version run as another user than the one that installs it, who has to read
// them.
func alignDirModes(root, like string) error {
	info, err := os.Stat(like)
	if err != nil {
		return fmt.Errorf("reading the mode of %s: %w", like, err)
	}

	mode := info.Mode().Perm()

	treeRoot, err := os.OpenRoot(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("setting the mode of the directories of %s: %w", root, err)
	}

	defer func() { _ = treeRoot.Close() }()

	err = fs.WalkDir(treeRoot.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return err
		}

		dirInfo, err := entry.Info()
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		if dirInfo.Mode().Perm() == mode {
			return nil
		}

		return treeRoot.Chmod(path, mode)
	})
	if err != nil {
		return fmt.Errorf("setting the mode of the directories of %s: %w", root, err)
	}

	return nil
}
