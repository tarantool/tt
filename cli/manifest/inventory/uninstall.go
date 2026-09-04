package inventory

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/state"
)

// UninstallOptions configures one uninstall run.
type UninstallOptions struct {
	// ProjectDir is the install-root for project scope (the caller's cwd). It
	// must be absolute; user and system scopes derive their roots from the
	// environment and ignore it.
	ProjectDir string
	// Scope selects which tree to remove from. The zero value ("") is project.
	Scope state.Scope
	// Package is the name of the guest package to remove.
	Package string
	// Yes skips the confirmation prompt.
	Yes bool
	// Confirm is asked before anything is removed; returning false aborts with
	// nothing touched. Nil proceeds (the CLI wires a real prompt; Yes bypasses it
	// entirely).
	Confirm func(prompt string) bool
	// Warn receives non-fatal diagnostics; nil drops them.
	Warn func(string)
}

func (o UninstallOptions) warn(msg string) {
	if o.Warn != nil {
		o.Warn(msg)
	}
}

// KeptDependency is a dependency the removed package brought that survives,
// because another installed package holds it too.
type KeptDependency struct {
	// Name is the rock name.
	Name string `json:"name" yaml:"name"`
	// Version is the version left in the tree.
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
	// HeldBy are the packages whose lock still pins it, in name order. It can be
	// empty when Installed is set: nothing references the rock, but it stands on
	// its own metadata.
	HeldBy []string `json:"held_by" yaml:"held_by"`
	// Installed marks a dependency that is also an installed package in its own
	// right. It is kept for a different reason than a shared rock — not because
	// something references it, but because only an uninstall naming it may
	// remove it — so the two are reported apart rather than conflated.
	Installed bool `json:"installed,omitempty" yaml:"installed,omitempty"`
}

// UninstallResult reports what an uninstall removed and what it deliberately
// left behind.
type UninstallResult struct {
	// Package is the removed package's name.
	Package string `json:"package" yaml:"package"`
	// Version is the version that was installed.
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
	// Scope is the tree it was removed from.
	Scope state.Scope `json:"scope" yaml:"scope"`
	// RemovedDependencies are the shared dependencies dropped along with it,
	// because nothing else owned them. In name order.
	RemovedDependencies []string `json:"removed_dependencies,omitempty" yaml:"removed_dependencies,omitempty"`
	// KeptDependencies are the ones another package still holds. In name order.
	KeptDependencies []KeptDependency `json:"kept_dependencies,omitempty" yaml:"kept_dependencies,omitempty"`
}

// Uninstall removes a guest package from a scope: its own files, its metadata,
// and every dependency it brought that nothing else still owns.
//
// Ownership is derived rather than counted: a dependency belongs to every
// installed package whose lock pins it, so after the target is set aside, a
// dependency with no remaining owner is dead and one with an owner stays. That
// is why removing the target's manifests/<pkg>/ directory is the only bookkeeping
// step — there is no separate counter to drift out of sync.
//
// Removal order matters for a re-run: files first, metadata last. An uninstall
// interrupted halfway leaves the metadata in place, so running it again finishes
// the job; the reverse would orphan files that nothing remembers owning.
func Uninstall(opts UninstallOptions) (*UninstallResult, error) {
	scope, lay, err := resolve(opts.Scope, opts.ProjectDir)
	if err != nil {
		return nil, err
	}

	// The name becomes a path, so its shape is checked before anything is
	// removed: a traversal-shaped or reserved argument must never reach RemoveAll.
	if !manifest.ValidPackageName(opts.Package) {
		return nil, stateErrorf("%w: %q", ErrBadPackageName, opts.Package)
	}

	packages, err := state.Packages(lay, scope)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	target, ok := state.Find(packages, opts.Package)
	if !ok {
		return nil, stateErrorf("%w: %q in %s scope", ErrNotInstalled, opts.Package, scope)
	}

	if target.Primary {
		return nil, stateErrorf("%w: %q is declared by %s in %s",
			ErrPrimaryPackage, target.Name, state.PrimaryManifestFile, lay.Root)
	}

	plan := planUninstall(lay, packages, target)

	// Removing a package another one locks is the user's call — the argument was
	// explicit — but it leaves that package's dependency unsatisfied, so say so
	// rather than letting it be discovered at runtime.
	if dependents := plan.dependents; len(dependents) > 0 {
		opts.warn(fmt.Sprintf("%s is still required by %s",
			target.Name, strings.Join(dependents, ", ")))
	}

	err = confirm(opts, plan)
	if err != nil {
		return nil, err
	}

	err = apply(opts, lay, plan)
	if err != nil {
		return nil, err
	}

	return plan.result(scope), nil
}

// removal is one directory set to delete: a package's or a dependency's files
// at a concrete version.
type removal struct {
	name    string
	version string
}

// uninstallPlan is everything one uninstall will do, decided before anything is
// written so the confirmation prompt describes the real outcome.
type uninstallPlan struct {
	// target is the package being removed, with the version found on disk.
	target removal
	// dead are the dependencies losing their last owner.
	dead []removal
	// kept are the dependencies another package still holds.
	kept []KeptDependency
	// dependents are the surviving packages whose lock pins the target itself —
	// they lose a dependency by this removal.
	dependents []string
}

// planUninstall decides what the uninstall removes. It reads only; the decision
// is fully made before apply touches the tree.
func planUninstall(
	lay state.Layout, packages []state.Package, target state.Package,
) uninstallPlan {
	survivors := make([]state.Package, 0, len(packages))

	for _, pkg := range packages {
		if pkg.Name != target.Name {
			survivors = append(survivors, pkg)
		}
	}

	plan := uninstallPlan{
		target: removal{
			name:    target.Name,
			version: onDiskVersion(lay, target.Name, target.Version),
		},
		dead: nil,
		kept: nil,
		// The target's own name is looked up the same way a dependency is: any
		// survivor pinning it loses a dependency when it goes.
		dependents: pinnedBy(survivors, target.Name),
	}

	brought := state.LockedDependencies(target.Lock)

	for _, dep := range sortedKeys(brought) {
		holders := pinnedBy(survivors, dep)
		_, installed := state.Find(survivors, dep)

		// A rock survives if anything still pins it, or if it is an installed
		// package in its own right — those are different reasons, and the result
		// reports which one applied.
		if len(holders) > 0 || installed {
			plan.kept = append(plan.kept, KeptDependency{
				Name:      dep,
				Version:   onDiskVersion(lay, dep, brought[dep]),
				HeldBy:    holders,
				Installed: installed,
			})

			continue
		}

		plan.dead = append(plan.dead, removal{
			name: dep, version: onDiskVersion(lay, dep, brought[dep]),
		})
	}

	return plan
}

// pinnedBy lists the packages whose lock pins dep, in name order.
func pinnedBy(survivors []state.Package, dep string) []string {
	var owners []string

	for _, pkg := range survivors {
		if _, pinned := state.LockedVersion(pkg.Lock, dep); pinned {
			owners = append(owners, pkg.Name)
		}
	}

	sort.Strings(owners)

	return owners
}

// onDiskVersion prefers the version actually present in the rocks tree over the
// one the lock recorded. They normally agree; when they do not — a dependency
// reconciled to another version by a later install — the tree is what has to be
// removed.
func onDiskVersion(lay state.Layout, name, recorded string) string {
	if installed, ok := state.InstalledVersion(lay, name); ok {
		return installed
	}

	return recorded
}

// confirm asks before anything is removed, describing the dependencies that go
// with the package. --yes and a nil Confirm proceed.
func confirm(opts UninstallOptions, plan uninstallPlan) error {
	if opts.Yes || opts.Confirm == nil {
		return nil
	}

	prompt := "remove " + plan.target.name

	if len(plan.dead) > 0 {
		names := make([]string, 0, len(plan.dead))
		for _, dep := range plan.dead {
			names = append(names, dep.name)
		}

		prompt += " and its now-unused dependencies " + strings.Join(names, ", ")
	}

	if !opts.Confirm(prompt + "?") {
		return stateErrorf("%w", ErrAborted)
	}

	return nil
}

// apply realizes the plan: the package's files, then the dead dependencies'
// files, then the metadata. Metadata goes last so an interrupted run can be
// repeated — the package still reads as installed until its files are gone.
func apply(opts UninstallOptions, lay state.Layout, plan uninstallPlan) error {
	err := removeFiles(lay, plan.target)
	if err != nil {
		return err
	}

	for _, dep := range plan.dead {
		opts.warn(fmt.Sprintf("removing unused dependency %s %s", dep.name, dep.version))

		err = removeFiles(lay, dep)
		if err != nil {
			return err
		}
	}

	err = state.RemoveMetadata(lay, plan.target.name)
	if err != nil {
		return stateErrorf("%w", err)
	}

	return nil
}

// removeFiles deletes one name's module trees and its rock-manifest directory.
// The rock root is pruned too, so no empty husk under rocks/<name>/ is left to
// make the rock look installed to the next plan.
func removeFiles(lay state.Layout, target removal) error {
	for _, path := range state.RockPaths(lay, target.name, target.version) {
		err := os.RemoveAll(path)
		if err != nil {
			return stateErrorf("removing %s %s: %w", target.name, target.version, err)
		}
	}

	// Prune the now-empty rock root so nothing under rocks/<name>/ makes the rock
	// look installed to the next plan. Remove() rather than RemoveAll(): a root
	// still holding another version directory must survive, and the ENOTEMPTY
	// that produces is the signal to leave it alone. Either way the failure is
	// cosmetic — an empty directory, not a broken tree — so it is not returned.
	_ = os.Remove(state.RockRoot(lay, target.name))

	return nil
}

// result projects the plan onto the reported outcome.
func (p uninstallPlan) result(scope state.Scope) *UninstallResult {
	removed := make([]string, 0, len(p.dead))
	for _, dep := range p.dead {
		removed = append(removed, dep.name)
	}

	result := &UninstallResult{
		Package:             p.target.name,
		Version:             p.target.version,
		Scope:               scope,
		RemovedDependencies: removed,
		KeptDependencies:    p.kept,
	}

	if len(removed) == 0 {
		result.RemovedDependencies = nil
	}

	return result
}
