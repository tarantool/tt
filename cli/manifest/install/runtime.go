package install

import (
	"path/filepath"

	"github.com/tarantool/go-luarocks/deps"

	"github.com/tarantool/tt/cli/manifest"
)

// runtimePath is the install-root's _runtime/ directory.
func runtimePath(lay layout) string {
	return filepath.Join(lay.root, runtimeDirName)
}

// versionHigher reports whether candidate is a strictly higher version than
// current. An unparseable or empty current counts as lower, so a first install
// (no recorded version) always upgrades.
func versionHigher(candidate, current string) bool {
	cand, err := deps.ParseVersion(candidate)
	if err != nil {
		return false
	}

	cur, err := deps.ParseVersion(current)
	if err != nil {
		return true
	}

	return deps.Compare(cand, cur) > 0
}

// selectProduct picks the product whose closure the archive's lock carries: the
// only product, or the one marked default. A build/pack always resolves against
// one product, so the archive lock normally holds exactly one.
func selectProduct(man *manifest.Manifest) (string, error) {
	if len(man.Products) == 1 {
		for name := range man.Products {
			return name, nil
		}
	}

	for name, prod := range man.Products {
		if prod.Default {
			return name, nil
		}
	}

	if len(man.Products) == 0 {
		return "", stateErrorf("archive manifest defines no products")
	}

	return "", stateErrorf("archive manifest has several products and none is default")
}

// checkRuntime validates the bundled runtime of a with-deps project install
// against the runtime the project already fixed. The primary package's
// [platform] constraints govern; when there is no primary package the first
// install sets the versions and later ones must match what is on disk.
//
// The on-disk runtime carries no queryable version, so the check is only as
// strong as the primary package's declared constraints: a bundled runtime that
// violates them is refused (exit 1); with no primary package and no constraints,
// the runtime is accepted and shared (the first install's tree is kept).
func checkRuntime(opts Options, header *Header, installed []installedPackage) error {
	if !header.WithDeps || opts.Scope != ScopeProject {
		return nil
	}

	primary, ok := findPrimary(installed)
	if !ok {
		return nil
	}

	plat := primary.manifest.Platform

	checks := []struct {
		name       string
		bundled    string
		constraint manifest.Constraint
	}{
		{"tarantool", header.Lock.BundledTarantool, plat.Tarantool},
		{"tt", header.Lock.BundledTt, plat.Tt},
		{"tcm", header.Lock.BundledTcm, plat.Tcm},
	}

	for _, check := range checks {
		if check.bundled == "" || check.constraint.Version == "" {
			continue
		}

		ok, err := runtimeSatisfies(check.bundled, check.constraint.Version)
		if err != nil {
			return err
		}

		if !ok {
			return stateErrorf(
				"%w: bundled %s %s does not satisfy the project's requirement %q (from %s)",
				errRuntimeMismatch, check.name, check.bundled, check.constraint.Version,
				primary.name)
		}
	}

	return nil
}

// findPrimary returns the project's primary package among the installed set.
func findPrimary(installed []installedPackage) (installedPackage, bool) {
	for _, pkg := range installed {
		if pkg.primary && pkg.manifest != nil {
			return pkg, true
		}
	}

	var zero installedPackage

	return zero, false
}

// runtimeSatisfies reports whether a concrete bundled version satisfies a
// constraint expression.
func runtimeSatisfies(version, constraint string) (bool, error) {
	parsed, err := deps.ParseVersion(version)
	if err != nil {
		return false, stateErrorf("unparseable bundled version %q: %w", version, err)
	}

	constraints, err := deps.ParseConstraints(constraint)
	if err != nil {
		return false, stateErrorf("unparseable platform constraint %q: %w", constraint, err)
	}

	return deps.Match(parsed, constraints), nil
}
