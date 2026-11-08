package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// stagePrefix names the directory of the project the archive's rocks are
// extracted into before they are grafted. Its name is not a rock tree's, so a
// crash that leaves one behind is not taken for anything.
const stagePrefix = ".tt-install-"

// graftArchive installs the rocks of the archive into the project's tree, in
// one transaction of it: the stale versions the plan replaces go first, then
// the rocks of the archive's .rocks/ are grafted, each staged from the files
// its rock_manifest lists, checked against it and installed as its binary rock
// would be (tree.Tx.Graft), with the command wrappers of this tree. A rock the
// plan keeps at the version installed is not grafted; one of another version
// replaces it, and the package's own rock replaces its other version.
//
// The archive's tree manifest is not trusted, and its files no rock lists are
// not grafted: in an archive an older tt packed, those are the files of the
// package itself, laid out by hand, which are installed as the package's rock
// here (rocktree.StageUntracked), so the project's tree owns them too.
func graftArchive(
	ctx context.Context, opts Options, archive *Archive, header *Header,
	lay state.Layout, installed []state.Package, plan installPlan, executable string,
) error {
	tmp, err := os.MkdirTemp(lay.Root, stagePrefix+"*")
	if err != nil {
		return fmt.Errorf("creating a staging directory in %s: %w", lay.Root, err)
	}

	defer func() { _ = os.RemoveAll(tmp) }()

	err = archive.Extract(tmp, rocksMapper())
	if err != nil {
		return fmt.Errorf("extracting %s: %w", archive.Path(), err)
	}

	src := filepath.Join(tmp, state.RocksDirName)

	srcView, err := tree.Inspect(ctx, src)
	if err != nil && !errors.Is(err, tree.ErrCorrupt) {
		return fmt.Errorf("reading the rocks of %s: %w", archive.Path(), err)
	}

	rocksTree, err := openTree(opts, lay, executable)
	if err != nil {
		return err
	}

	graft := guestGraft{
		opts: opts, header: header, lay: lay, installed: installed, plan: plan,
		src: src, srcView: srcView, stage: filepath.Join(tmp, "stage"),
	}

	err = rocksTree.Update(ctx, func(change *tree.Tx) error {
		return graft.run(change, rocksTree.Tree)
	})
	if err != nil {
		return stateErrorf("installing the rocks of %s: %w", archive.Path(), err)
	}

	return nil
}

// guestGraft is one graftArchive.
type guestGraft struct {
	opts      Options
	header    *Header
	lay       state.Layout
	installed []state.Package
	plan      installPlan
	// src is the archive's .rocks/, extracted; srcView reads it, nil when its
	// manifest does not parse, which the graft does not read.
	src     string
	srcView *tree.View
	// stage is where the files of the package an older tt packed are laid out.
	stage string
}

// run is graftArchive in the transaction change of the tree rooted at root.
func (g guestGraft) run(change *tree.Tx, root string) error {
	err := removeStale(change, g.lay, g.plan)
	if err != nil {
		return err
	}

	name := g.header.Manifest.Package.Name

	err = g.removeLegacyGuest(change, name)
	if err != nil {
		return err
	}

	err = g.prepareGuest(change, root, name)
	if err != nil {
		return err
	}

	report, err := change.Graft(g.src, tree.GraftOpts{
		Select: func(ref tree.RockRef) bool {
			_, skip := g.plan.skipNames[ref.Name]

			return !skip
		},
		Replace:             true,
		RemoveOtherVersions: true,
	})
	if err != nil {
		return fmt.Errorf("grafting the rocks: %w", err)
	}

	return g.installUntracked(change, root, report.Untracked)
}

// removeLegacyGuest removes the files of the guest name an older tt
// installed, which are no rock of the tree, as that tt removed them: its
// share/ and lib/ subtrees.
func (g guestGraft) removeLegacyGuest(change *tree.Tx, name string) error {
	guest, ok := state.Find(g.installed, name)
	if !ok || len(change.View().Versions(name)) > 0 {
		return nil
	}

	err := state.RemoveUntracked(g.lay, name, guest.Version)
	if err != nil {
		return stateErrorf("%w", err)
	}

	return nil
}

// prepareGuest makes room for the rock of the guest name the archive carries,
// if any (rocktree.PreparePackage).
func (g guestGraft) prepareGuest(change *tree.Tx, root, name string) error {
	if g.srcView == nil {
		return nil
	}

	for _, version := range g.srcView.Versions(name) {
		err := rocktree.PreparePackage(change, root, g.srcView,
			tree.RockRef{Name: name, Version: version})
		if err != nil {
			return err
		}
	}

	return nil
}

// installUntracked installs the files of the archive's tree no rock lists as
// the rock of the guest, when there are any of share/tarantool/, lib/tarantool/
// and bin/.
func (g guestGraft) installUntracked(change *tree.Tx, root string, untracked []string) error {
	staged, err := rocktree.StageUntracked(g.src, untracked, g.stage)
	if err != nil {
		return err
	}

	if staged == 0 {
		return nil
	}

	man := g.header.Manifest

	productName, err := selectProduct(man)
	if err != nil {
		return err
	}

	spec, err := rocktree.Rockspec(rocktree.Package{
		Name:    man.Package.Name,
		Version: g.header.Version,
		Summary: man.Package.Description,
		Dependencies: rocktree.PackageDependencies(man, productName,
			g.header.Lock.Products[productName]),
	})
	if err != nil {
		return stateErrorf("%w", err)
	}

	g.opts.warn(fmt.Sprintf("%s was packed by a tt that did not pack it as a rock; "+
		"its %d files are installed as the rock %s %s", man.Package.Name, staged,
		spec.Package, spec.Version))

	return rocktree.InstallPackage(change, root, spec, g.stage)
}

// rocksMapper builds the mapper that extracts the .rocks/ of an archive alone,
// tt's own install state aside, which never travels in an archive and is
// dropped if it does.
func rocksMapper() func(string) (string, bool) {
	return func(name string) (string, bool) {
		top, rest, _ := strings.Cut(name, "/")
		if top != state.RocksDirName || rest == "" {
			return "", false
		}

		if first, _, _ := strings.Cut(rest, "/"); first == state.ManifestsDirName {
			return "", false
		}

		return name, true
	}
}
