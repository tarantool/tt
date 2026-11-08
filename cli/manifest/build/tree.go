package build

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"slices"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
	"github.com/tarantool/tt/v3/cli/manifest/state"
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

// pruneTree removes from the project's rocks tree, after the lock's closure is
// installed, every version of a rock the lock pins other than the one it
// pins, and the rock dirs the tree manifest does not list (what an
// interrupted install leaves). The native client keeps the other versions of
// a rock it installs, so without it a lock that moves a rock to another
// version would leave both in the tree, the newer one holding the plain paths
// of their modules whichever the lock pins.
//
// What the lock does not pin is kept: the rock of the package itself, which
// its install replaces whole; the guest packages installed into the project
// and every rock their locks pin, at the version the install reconciled them
// to; and any other rock, such as the luatest tt test installs when the
// manifest declares none, which a build would otherwise remove only for the
// next tt test to fetch it again. extra pins more rocks, at the versions a
// caller resolved for them. log is told of each version removed.
func pruneTree(
	ctx context.Context, cfg luarocks.Config, projectDir string, lock *manifest.Lock,
	extra []manifest.LockDependency, log func(string),
) error {
	_, err := os.Stat(cfg.Tree)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	keep, err := keepRocks(projectDir, lock, extra)
	if err != nil {
		return err
	}

	rocksTree, err := openTree(cfg)
	if err != nil {
		return err
	}

	var removed []tree.RockRef

	err = rocksTree.Update(ctx, func(change *tree.Tx) error {
		var err error

		removed, err = change.Prune(keep, tree.PruneOpts{Orphans: true})

		return err //nolint:wrapcheck // Wrapped with the Update's error below.
	})
	if err != nil {
		return fmt.Errorf("pruning the rocks tree %s: %w", cfg.Tree, err)
	}

	if log != nil {
		for _, ref := range removed {
			log(fmt.Sprintf("removed %s %s, which the lock does not pin", ref.Name, ref.Version))
		}
	}

	return nil
}

// keepRocks returns the keep function of pruneTree.
func keepRocks(
	projectDir string, lock *manifest.Lock, extra []manifest.LockDependency,
) (func(tree.RockRef) bool, error) {
	pins := map[string][]string{}

	pin := func(deps []manifest.LockDependency) {
		for _, dep := range deps {
			pins[dep.Name] = append(pins[dep.Name], dep.Version)
		}
	}

	anyVersion := map[string]bool{}

	if lock != nil {
		for _, product := range lock.Products {
			pin(product.Dependencies)
		}

		pin(lock.DevDependencies)
	}

	pin(extra)

	packages, err := state.Packages(state.ProjectLayout(projectDir))
	if err != nil {
		return nil, fmt.Errorf("reading the packages of %s: %w", projectDir, err)
	}

	for _, pkg := range packages {
		anyVersion[pkg.Name] = true

		if pkg.Primary {
			continue
		}

		for name := range state.LockedDependencies(pkg.Lock) {
			anyVersion[name] = true
		}
	}

	return func(ref tree.RockRef) bool {
		versions, pinned := pins[ref.Name]
		if anyVersion[ref.Name] || !pinned {
			return true
		}

		return slices.ContainsFunc(versions, func(version string) bool {
			return state.SameVersion(ref.Version, version)
		})
	}, nil
}

// logInfo returns a log function writing to logger at the info level, nil
// for a nil logger.
func logInfo(logger *slog.Logger) func(string) {
	if logger == nil {
		return nil
	}

	return func(msg string) { logger.Info(msg) }
}
