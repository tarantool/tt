package build

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/sdk/v3/log"
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

// pruneTree brings the project's rocks tree in step with the lock, after the
// lock's closure is installed. It removes:
//
//   - every rock the lock of the project held when tt last materialized the
//     tree (state.ReadMaterialized) and holds no more: a dependency tt package
//     remove dropped, or one a lock moved to another version stopped pulling
//     in. Left in the tree, it would still be requirable;
//   - every version of a rock the lock pins other than the one it pins. The
//     native client keeps the other versions of a rock it installs, so a lock
//     that moves a rock to another version would leave both in the tree, the
//     newer one holding the plain paths of their modules whichever the lock
//     pins;
//   - the rock dirs the tree manifest does not list, what an interrupted
//     install leaves.
//
// A rock is removed as tt package uninstall removes a dependency: by the tree,
// which deletes the files its rock_manifest lists and drops it from the tree
// manifest.
//
// What the lock does not decide is kept: the rock of the package itself,
// which its install replaces whole; the guest packages installed into the
// project and every rock their locks pin, at the version the install
// reconciled them to, which is how uninstall tells the dependencies a guest
// still holds; and any rock the lock never held, such as the luatest tt test
// installs when the manifest declares none, which a build would otherwise
// remove only for the next tt test to fetch it again. extra pins more rocks,
// at the versions a caller resolved for them; they are not recorded as the
// lock's.
//
// Once the tree is pruned, the rocks the lock pins are recorded as the ones
// the tree was materialized from (state.WriteMaterialized). report is told of
// each rock version removed.
func pruneTree(
	ctx context.Context, cfg luarocks.Config, projectDir string, lock *manifest.Lock,
	extra []manifest.LockDependency, report func(prunedRock),
) error {
	_, err := os.Stat(cfg.Tree)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	lay := state.ProjectLayout(projectDir)

	rule, err := newPruneRule(lay, lock, extra)
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

		removed, err = change.Prune(rule.keep, tree.PruneOpts{Orphans: true})

		return err //nolint:wrapcheck // Wrapped with the Update's error below.
	})
	if err != nil {
		return fmt.Errorf("pruning the rocks tree %s: %w", cfg.Tree, err)
	}

	err = state.WriteMaterialized(lay, lockedNames(lock))
	if err != nil {
		return fmt.Errorf("pruning the rocks tree %s: %w", cfg.Tree, err)
	}

	if report != nil {
		for _, ref := range removed {
			report(rule.pruned(ref))
		}
	}

	return nil
}

// pruneReason is why pruneTree removed a rock version.
type pruneReason int

const (
	// prunedDropped is a rock the lock held and holds no more.
	prunedDropped pruneReason = iota
	// prunedReplaced is a version of a rock the lock pins at another one.
	prunedReplaced
	// prunedOrphan is a rock dir the tree manifest does not list.
	prunedOrphan
)

// prunedRock is one rock version pruneTree removed.
type prunedRock struct {
	// Ref is the rock version removed.
	Ref tree.RockRef
	// Reason is why.
	Reason pruneReason
	// Pinned is the version the lock pins, of a replaced rock.
	Pinned string
}

// String is the line that tells the user of the removal.
func (p prunedRock) String() string {
	switch p.Reason {
	case prunedDropped:
		return fmt.Sprintf("removed unused dependency %s %s", p.Ref.Name, p.Ref.Version)
	case prunedReplaced:
		return fmt.Sprintf("removed %s %s, the lock pins %s", p.Ref.Name, p.Ref.Version,
			p.Pinned)
	default:
		return fmt.Sprintf("removed %s %s, a rock dir the tree manifest does not list",
			p.Ref.Name, p.Ref.Version)
	}
}

// pruneRule decides which rocks of the tree pruneTree keeps (keep), and tells
// why it removed one (pruned).
type pruneRule struct {
	// pins are the versions the lock and extra pin, by rock.
	pins map[string][]string
	// held are the rocks kept at any version: the packages of the project,
	// the guests among them, and every rock a guest's lock pins.
	held map[string]bool
	// dropped are the rocks the lock held at the last materialization and
	// pins no more.
	dropped map[string]bool
	// doomed are the rock versions keep refused, which Prune removes.
	doomed map[tree.RockRef]bool
}

// newPruneRule reads the rule of pruneTree for the project of lay.
func newPruneRule(
	lay state.Layout, lock *manifest.Lock, extra []manifest.LockDependency,
) (pruneRule, error) {
	rule := pruneRule{
		pins: map[string][]string{}, held: map[string]bool{}, dropped: map[string]bool{},
		doomed: map[tree.RockRef]bool{},
	}

	pin := func(deps []manifest.LockDependency) {
		for _, dep := range deps {
			rule.pins[dep.Name] = append(rule.pins[dep.Name], dep.Version)
		}
	}

	if lock != nil {
		for _, product := range lock.Products {
			pin(product.Dependencies)
		}

		pin(lock.DevDependencies)
	}

	pin(extra)

	packages, err := state.Packages(lay)
	if err != nil {
		return pruneRule{}, fmt.Errorf("reading the packages of %s: %w", lay.Root, err)
	}

	for _, pkg := range packages {
		rule.held[pkg.Name] = true

		if pkg.Primary {
			continue
		}

		// The rocks a guest's lock pins are the guest's, as tt package
		// uninstall counts them: they go with the last package that pins them.
		for name := range state.LockedDependencies(pkg.Lock) {
			rule.held[name] = true
		}
	}

	// A tree with no record has nothing the lock is known to have dropped.
	materialized, _, err := state.ReadMaterialized(lay)
	if err != nil {
		return pruneRule{}, fmt.Errorf("reading what the tree was materialized from: %w", err)
	}

	for name := range materialized {
		if _, pinned := rule.pins[name]; !pinned {
			rule.dropped[name] = true
		}
	}

	return rule, nil
}

// keep is the keep function of Prune.
func (r pruneRule) keep(ref tree.RockRef) bool {
	kept := r.keeps(ref)
	if !kept {
		r.doomed[ref] = true
	}

	return kept
}

// keeps is keep, with nothing recorded.
func (r pruneRule) keeps(ref tree.RockRef) bool {
	if r.held[ref.Name] {
		return true
	}

	versions, pinned := r.pins[ref.Name]
	if !pinned {
		return !r.dropped[ref.Name]
	}

	return slices.ContainsFunc(versions, func(version string) bool {
		return state.SameVersion(ref.Version, version)
	})
}

// pruned tells why Prune removed ref.
func (r pruneRule) pruned(ref tree.RockRef) prunedRock {
	switch versions, pinned := r.pins[ref.Name]; {
	case !r.doomed[ref]:
		return prunedRock{Ref: ref, Reason: prunedOrphan, Pinned: ""}
	case pinned:
		return prunedRock{Ref: ref, Reason: prunedReplaced, Pinned: versions[0]}
	default:
		return prunedRock{Ref: ref, Reason: prunedDropped, Pinned: ""}
	}
}

// lockedNames are the rocks lock pins, in its products and its dev closure.
func lockedNames(lock *manifest.Lock) []string {
	if lock == nil {
		return nil
	}

	var names []string

	for _, product := range lock.Products {
		for _, dep := range product.Dependencies {
			names = append(names, dep.Name)
		}
	}

	for _, dep := range lock.DevDependencies {
		names = append(names, dep.Name)
	}

	return names
}

// pruneReport tells of what pruneTree removed in a run (report): what the
// lock no longer holds, and the versions it replaced, go to removed, a line
// each; a rock dir the tree manifest does not list is tt's own record, at the
// debug level tt -V shows, as the logger of go-luarocks has the records of the
// library it gets at info. With no removed, everything is.
type pruneReport struct {
	// removed is Options.Removed of the run.
	removed func(string)
}

// report is the report function of pruneTree.
func (r pruneReport) report(rock prunedRock) {
	if rock.Reason == prunedOrphan || r.removed == nil {
		log.Debug(rock.String())

		return
	}

	r.removed(rock.String())
}
