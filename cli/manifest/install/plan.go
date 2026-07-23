package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/go-luarocks/deps"

	"github.com/tarantool/tt/cli/manifest"
)

// depSource says where a dependency's files come from in the resolved plan.
type depSource int

const (
	// fromArchive extracts the dependency's subtree straight from the .tt archive
	// (a with-deps install of the version the archive already carries).
	fromArchive depSource = iota
	// fromRegistry refetches the pinned version from the registry (a
	// --without-deps install, or a project whose reconciled version the archive
	// does not carry).
	fromRegistry
	// skipDep leaves the dependency untouched: it is already installed at the
	// reconciled version.
	skipDep
)

// depDecision is the plan for one dependency of the package being installed.
type depDecision struct {
	name string
	// version is the reconciled version to end up on disk.
	version string
	// source says how to realize that version.
	source depSource
	// removeVersion is a stale on-disk version directory to delete first, empty
	// when nothing is being replaced.
	removeVersion string
}

// installPlan is the resolved decision for every registry dependency of the
// package being installed.
type installPlan struct {
	decisions []depDecision
	// skipNames is the set of dependency names whose files must NOT be taken
	// from the archive (their reconciled version stays whatever is on disk).
	skipNames map[string]struct{}
}

// planDeps reconciles every registry dependency the package brings against what
// is already installed and decides, per dependency, whether to extract it from
// the archive, refetch it from the registry, or leave the installed copy in
// place. withDeps selects the archive vs registry source for a dependency the
// installed tree does not already satisfy.
func planDeps(
	header *Header, productName string, installed []installedPackage, lay layout, withDeps bool,
) (installPlan, error) {
	prod, ok := header.Lock.Products[productName]
	if !ok {
		var zero installPlan

		return zero, stateErrorf("archive lock has no closure for product %q", productName)
	}

	plan := installPlan{decisions: nil, skipNames: map[string]struct{}{}}

	for _, dep := range prod.Dependencies {
		if dep.Source != sourceRegistry {
			// Path dependencies are the package's own sub-projects; a with-deps
			// archive carries them, and there is no registry to refetch them from.
			if withDeps {
				plan.decisions = append(plan.decisions, depDecision{
					name: dep.Name, version: dep.Version, source: fromArchive, removeVersion: "",
				})
			}

			continue
		}

		decision, err := planRegistryDep(dep, header, installed, lay, withDeps)
		if err != nil {
			var zero installPlan

			return zero, err
		}

		plan.decisions = append(plan.decisions, decision)

		if decision.source == skipDep {
			plan.skipNames[dep.Name] = struct{}{}
		}
	}

	return plan, nil
}

// planRegistryDep reconciles one registry dependency and decides its source.
func planRegistryDep(
	dep manifest.LockDependency, header *Header,
	installed []installedPackage, lay layout, withDeps bool,
) (depDecision, error) {
	contributions := collectContributions(dep.Name, dep.Version, header.Manifest, installed)

	winner, err := reconcile(dep.Name, contributions)
	if err != nil {
		var zero depDecision

		return zero, err
	}

	onDisk, hasOnDisk := installedVersion(lay, dep.Name)

	switch {
	case winner == dep.Version:
		// The archive carries the winning version. Replace a differing installed
		// copy; a with-deps archive extracts it, a --without-deps one refetches it.
		remove := ""
		if hasOnDisk && !sameVersion(onDisk, winner) {
			remove = onDisk
		}

		src := fromRegistry
		if withDeps {
			src = fromArchive
		}

		return depDecision{name: dep.Name, version: winner, source: src, removeVersion: remove}, nil
	case hasOnDisk && sameVersion(onDisk, winner):
		// The reconciled version is already installed; leave it in place.
		return depDecision{
			name: dep.Name, version: winner, source: skipDep, removeVersion: "",
		}, nil
	default:
		// The winner is neither the archive's version nor what is on disk. Only
		// the registry can supply it; a with-deps offline install cannot.
		if withDeps {
			var zero depDecision

			return zero, stateErrorf(
				"%w: reconciled %s to %s, which this offline archive does not carry",
				errIncompatibleDeps, dep.Name, winner)
		}

		remove := ""
		if hasOnDisk {
			remove = onDisk
		}

		return depDecision{
			name: dep.Name, version: winner, source: fromRegistry, removeVersion: remove,
		}, nil
	}
}

// collectContributions gathers the pin and declared constraint every relevant
// package puts on a shared dependency: the package being installed, plus every
// already-installed package whose lock also pins it.
func collectContributions(
	dep, newPin string, newManifest *manifest.Manifest, installed []installedPackage,
) []contribution {
	contributions := []contribution{{
		pkg:        newManifest.Package.Name,
		pin:        newPin,
		constraint: declaredConstraint(newManifest, dep),
	}}

	for _, pkg := range installed {
		if pkg.lock == nil {
			continue
		}

		pin, ok := lockedVersion(pkg.lock, dep)
		if !ok {
			continue
		}

		contributions = append(contributions, contribution{
			pkg:        pkg.name,
			pin:        pin,
			constraint: declaredConstraint(pkg.manifest, dep),
		})
	}

	return contributions
}

// declaredConstraint returns the version constraint a manifest declares for a
// dependency, across the global map and every component map. Multiple
// declarations are AND-ed by comma-joining, matching the resolver. An unknown
// or path dependency contributes no constraint.
func declaredConstraint(man *manifest.Manifest, dep string) string {
	if man == nil {
		return ""
	}

	var parts []string

	collect := func(deps map[string]manifest.Dependency) {
		if decl, ok := deps[dep]; ok && decl.Source != sourcePath && decl.Version != "" {
			parts = append(parts, decl.Version)
		}
	}

	collect(man.Dependencies)

	for _, comp := range man.Components {
		collect(comp.Dependencies)
	}

	return joinConstraints(parts)
}

// joinConstraints comma-joins constraint expressions into one, dropping empties.
func joinConstraints(parts []string) string {
	out := ""

	for _, part := range parts {
		if part == "" {
			continue
		}

		if out == "" {
			out = part
		} else {
			out += "," + part
		}
	}

	return out
}

// lockedVersion returns the version a lock pins for a dependency across all its
// products.
func lockedVersion(lock *manifest.Lock, dep string) (string, bool) {
	for _, prod := range lock.Products {
		for _, d := range prod.Dependencies {
			if d.Name == dep {
				return d.Version, true
			}
		}
	}

	return "", false
}

// installedVersion reads the version of a rock currently installed in the tree
// from <tree>/share/tarantool/rocks/<dep>/<version>/. It returns the first
// version directory found; a well-formed tree holds exactly one per rock.
func installedVersion(lay layout, dep string) (string, bool) {
	rocksDir := filepath.Join(lay.share, "rocks", dep)

	entries, err := os.ReadDir(rocksDir)
	if err != nil {
		return "", false
	}

	for _, entry := range entries {
		if entry.IsDir() {
			return entry.Name(), true
		}
	}

	return "", false
}

// sameVersion reports whether two version strings denote the same version,
// tolerating formatting differences (a trailing "-1" revision, say).
func sameVersion(left, right string) bool {
	if left == right {
		return true
	}

	parsedLeft, err := deps.ParseVersion(left)
	if err != nil {
		return false
	}

	parsedRight, err := deps.ParseVersion(right)
	if err != nil {
		return false
	}

	return deps.Compare(parsedLeft, parsedRight) == 0
}

// rockRemovePaths lists the on-disk paths that hold one version of a rock: its
// module trees under share/ and lib/, and its rock-manifest directory.
func rockRemovePaths(lay layout, dep, version string) []string {
	return []string{
		filepath.Join(lay.share, dep),
		filepath.Join(lay.lib, dep),
		filepath.Join(lay.share, "rocks", dep, version),
	}
}

// removeStaleVersions deletes the on-disk files of any dependency version the
// plan is replacing, so a reconciled upgrade does not leave two versions of a
// rock side by side.
func removeStaleVersions(lay layout, plan installPlan) error {
	for _, decision := range plan.decisions {
		if decision.removeVersion == "" {
			continue
		}

		for _, stalePath := range rockRemovePaths(lay, decision.name, decision.removeVersion) {
			err := os.RemoveAll(stalePath)
			if err != nil {
				return fmt.Errorf("removing stale %s %s: %w",
					decision.name, decision.removeVersion, err)
			}
		}
	}

	return nil
}
