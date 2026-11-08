package build

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

// openTree opens the project's rocks tree of cfg with the command wrappers
// the native backend writes for it, so that a rock tt installs through the
// tree gets the same ones as a rock the client installs.
func openTree(cfg luarocks.Config) (*tree.Tree, error) {
	rocksTree, err := tree.Open(cfg)
	if err != nil {
		return nil, fmt.Errorf("opening the rocks tree %s: %w", cfg.Tree, err)
	}

	rocksTree.BinWrap = client.BinWrapFor(cfg)

	return rocksTree, nil
}

// repairTree rebuilds the manifest of the project's rocks tree from the rocks
// on disk when it is out of step with them, before anything is installed
// into it (rocktree.Repair): a tt that changed the tree behind the
// manifest's back left rocks the manifest does not list, and entries without
// their rocks, which the rocks installed next would trip over.
func repairTree(ctx context.Context, cfg luarocks.Config, warn func(string)) error {
	_, err := os.Stat(cfg.Tree)
	if errors.Is(err, fs.ErrNotExist) {
		// Nothing was ever installed: there is nothing to repair, and a
		// package that installs nothing gets no tree.
		return nil
	}

	rocksTree, err := openTree(cfg)
	if err != nil {
		return err
	}

	err = rocktree.Repair(ctx, rocksTree, warn)
	if err != nil {
		return fmt.Errorf("repairing the rocks tree: %w", err)
	}

	return nil
}
