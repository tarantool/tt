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
// backend writes for the Tarantool of opts, so that a rock the install puts
// there through the tree gets the same ones as a rock it fetches.
func openTree(opts Options, lay state.Layout) (*tree.Tree, error) {
	cfg := rocks.BuildConfig(opts.Tarantool, rocks.ConfigOptions{
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

	rocksTree, err := openTree(opts, lay)
	if err != nil {
		return err
	}

	err = rocktree.Repair(ctx, rocksTree, opts.Warn)
	if err != nil {
		return fmt.Errorf("repairing the rocks tree: %w", err)
	}

	return nil
}
