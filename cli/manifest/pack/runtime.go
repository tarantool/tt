package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/go-version"

	"github.com/tarantool/tt/cli/manifest"
)

// Runtime component directory names inside _runtime/.
const (
	runtimeTarantool = "tarantool"
	runtimeTt        = "tt"
	runtimeTcm       = "tcm"
)

// Flavor tokens, matching manifest.Constraint.EffectiveFlavor.
const (
	flavorCE = "ce"
	flavorEE = "ee"
)

// tarantoolLicenseNames are the file names a bundled Tarantool's license may
// carry. Shipping the binary without one is refused.
var tarantoolLicenseNames = []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING"}

// runtimeSource describes one resolved runtime component ready to be bundled.
type runtimeSource struct {
	// Name is the _runtime/ subdirectory: tarantool, tt or tcm.
	Name string
	// Version is the concrete version bundled, recorded in the lock's
	// bundled_*_version field.
	Version string
	// Dir is a directory tree to copy wholesale (a runtime cache entry).
	// Exactly one of Dir and Binary is set.
	Dir string
	// Binary is a single executable to copy into <name>/bin/.
	Binary string
	// Fallback marks a component resolved from the active tt environment
	// rather than the runtime cache, which is worth warning about.
	Fallback bool
}

// RuntimeOptions carries what bundleRuntime needs to find and place runtimes.
type RuntimeOptions struct {
	// CacheDir is the runtime cache root; entries live at
	// <CacheDir>/<component>/<version>/.
	CacheDir string
	// Platform is the manifest's [platform] block, whose constraints select the
	// versions to bundle. Run fills this from the built manifest; callers leave
	// it zero.
	Platform manifest.Platform
	// ActiveTarantool / ActiveTt / ActiveTcm are the binaries of the running tt
	// environment, used as the fallback when the cache has no match. The Flavor
	// fields carry "ce", "ee", or "" when the flavor could not be determined;
	// an unknown flavor is only ever accepted for a [ce] requirement.
	ActiveTarantool        string
	ActiveTarantoolVersion string
	ActiveTarantoolFlavor  string
	ActiveTt               string
	ActiveTtVersion        string
	ActiveTtFlavor         string
	ActiveTcm              string
	ActiveTcmVersion       string
	// Warn receives the fallback diagnostics; nil drops them.
	Warn func(string)
}

// BundledVersions reports the concrete versions that went into _runtime/, to be
// stamped into the lock.
type BundledVersions struct {
	Tarantool string
	Tt        string
	Tcm       string
}

// bundleRuntime resolves and copies _runtime/ into stageDir, returning the
// versions bundled. TCM is bundled only when [platform].tcm is set.
//
// Each component is resolved cache-first: the highest version in the runtime
// cache satisfying the constraint wins. Failing that, the active tt
// environment's own binary is accepted if it satisfies the constraint, with a
// warning — the cache is the reproducible source, the active binary is a
// convenience for v0 where no `tt runtime install` exists yet.
func bundleRuntime(stageDir string, req RuntimeOptions) (BundledVersions, error) {
	// [platform].tarantool and .tt are required by manifest validation, so an
	// empty constraint here means the platform block never reached this call.
	// Without the guard that mistake produces a with-deps archive carrying no
	// _runtime/ at all - silently, and indistinguishable from --without-deps.
	if req.Platform.Tarantool.IsZero() || req.Platform.Tt.IsZero() {
		return BundledVersions{}, stateErrorf(
			"%w: [platform].tarantool and [platform].tt are required to bundle a runtime",
			errNoRuntime)
	}

	wanted := []struct {
		name       string
		constraint manifest.Constraint
		binary     string
		binVersion string
		binFlavor  string
	}{
		{
			runtimeTarantool, req.Platform.Tarantool,
			req.ActiveTarantool, req.ActiveTarantoolVersion, req.ActiveTarantoolFlavor,
		},
		{
			runtimeTt, req.Platform.Tt,
			req.ActiveTt, req.ActiveTtVersion, req.ActiveTtFlavor,
		},
		// TCM is Enterprise-only and carries no flavor (manifest validation
		// rejects one), so its effective flavor is never compared.
		{runtimeTcm, req.Platform.Tcm, req.ActiveTcm, req.ActiveTcmVersion, ""},
	}

	var bundled BundledVersions

	for _, w := range wanted {
		if w.constraint.IsZero() {
			// Only tcm is optional; tarantool and tt are required by Validate.
			continue
		}

		src, err := resolveRuntime(req, w.name, w.constraint,
			activeBinary{path: w.binary, version: w.binVersion, flavor: w.binFlavor})
		if err != nil {
			return BundledVersions{}, err
		}

		if src.Fallback && req.Warn != nil {
			req.Warn(fmt.Sprintf(
				"no %s %s in the runtime cache (%s); bundling the active %s %s instead",
				w.name, w.constraint.Version, req.CacheDir, w.name, src.Version))
		}

		// A prerelease satisfies the constraint here on its core version alone
		// (see coreVersion), so say so rather than letting a development build
		// end up in a release archive unremarked.
		if isPrerelease(src.Version) && req.Warn != nil {
			req.Warn(fmt.Sprintf(
				"bundling %s %s, a prerelease build, to satisfy %q",
				w.name, src.Version, w.constraint.Version))
		}

		if err := placeRuntime(stageDir, src); err != nil {
			return BundledVersions{}, err
		}

		switch w.name {
		case runtimeTarantool:
			bundled.Tarantool = src.Version
		case runtimeTt:
			bundled.Tt = src.Version
		case runtimeTcm:
			bundled.Tcm = src.Version
		}
	}

	return bundled, nil
}

// activeBinary describes the running tt environment's copy of one component,
// the fallback source when the cache has no match.
type activeBinary struct {
	// path is the executable; empty means the component was not found.
	path string
	// version is its reported version; empty means it could not be determined,
	// which never satisfies a constraint.
	version string
	// flavor is "ce", "ee", or "" for undetermined.
	flavor string
}

// resolveRuntime picks the source for one runtime component: cache first,
// active binary second. Both must match the constraint's flavor as well as its
// version - bundling a CE build for an [ee] requirement produces an archive
// that is wrong in a way nothing downstream detects.
func resolveRuntime(
	req RuntimeOptions, name string, constraint manifest.Constraint,
	active activeBinary,
) (runtimeSource, error) {
	flavor := constraint.EffectiveFlavor()

	dir, ver, ok, err := findInCache(req.CacheDir, name, flavor, constraint)
	if err != nil {
		return runtimeSource{}, err
	}

	if ok {
		return runtimeSource{Name: name, Version: ver, Dir: dir}, nil
	}

	usable, err := activeUsable(active, flavor, constraint)
	if err != nil {
		return runtimeSource{}, err
	}

	if usable {
		return runtimeSource{
			Name:     name,
			Version:  normalizeVersion(active.version),
			Binary:   active.path,
			Fallback: true,
		}, nil
	}

	return runtimeSource{}, stateErrorf(
		"%w: no %s satisfying %s found in the runtime cache %s, and the active "+
			"%s (%s) does not satisfy it; place a matching build under %s",
		errNoRuntime, name, constraint.String(), req.CacheDir, name,
		describeActive(active),
		filepath.Join(req.CacheDir, name, flavor, "<version>"))
}

// activeUsable reports whether the active binary may stand in for the cache.
// An undetermined flavor is accepted only for a [ce] requirement: ce is the
// default and overwhelmingly the common case, whereas silently treating an
// unverified build as Enterprise would be a licensing error, not just a bug.
func activeUsable(
	active activeBinary, flavor string, constraint manifest.Constraint,
) (bool, error) {
	if active.path == "" {
		return false, nil
	}

	versionOK, err := satisfies(active.version, constraint)
	if err != nil {
		return false, err
	}

	if !versionOK {
		return false, nil
	}

	switch active.flavor {
	case flavor:
		return true, nil
	case "":
		return flavor == flavorCE, nil
	default:
		return false, nil
	}
}

// describeActive renders the active binary for an error message, saying plainly
// which of the two checks could not be made rather than implying both ran.
func describeActive(active activeBinary) string {
	switch {
	case active.path == "":
		return "not found"
	case active.version == "":
		return active.path + ", version undetermined"
	case active.flavor == "":
		return active.version + ", flavor undetermined"
	default:
		return active.version + "[" + active.flavor + "]"
	}
}

// findInCache returns the highest cached version of a component satisfying the
// constraint. Cache entries are directories laid out as
// <cache>/<component>/<flavor>/<version>/, so a CE and an EE build of the same
// version can coexist and a [ce] requirement can never resolve to an EE tree.
func findInCache(
	cacheDir, name, flavor string, constraint manifest.Constraint,
) (string, string, bool, error) {
	if cacheDir == "" {
		return "", "", false, nil
	}

	root := filepath.Join(cacheDir, name, flavor)

	entries, err := os.ReadDir(root)
	if err != nil {
		// A missing or unreadable cache is a miss, not a failure: the fallback
		// may still supply the component, and an absent cache is the normal
		// state in v0 where nothing populates it automatically.
		return "", "", false, nil //nolint:nilerr // An unreadable cache is a miss.
	}

	var matches []string

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		ok, err := satisfies(e.Name(), constraint)
		if err != nil {
			return "", "", false, err
		}

		if ok {
			matches = append(matches, e.Name())
		}
	}

	if len(matches) == 0 {
		return "", "", false, nil
	}

	sortVersionsDesc(matches)

	return filepath.Join(root, matches[0]), normalizeVersion(matches[0]), true, nil
}

// sortVersionsDesc sorts semantic versions highest-first, falling back to
// reverse lexical order for anything unparseable.
func sortVersionsDesc(versions []string) {
	sort.Slice(versions, func(i, j int) bool {
		vi, erri := version.NewVersion(normalizeVersion(versions[i]))
		vj, errj := version.NewVersion(normalizeVersion(versions[j]))

		if erri != nil || errj != nil {
			return versions[i] > versions[j]
		}

		return vi.GreaterThan(vj)
	})
}

// satisfies reports whether a concrete version meets the manifest constraint.
// An empty constraint accepts anything.
//
// A constraint tt cannot parse is an error rather than a miss: degrading it to
// a literal comparison would quietly bundle the wrong runtime, or nothing at
// all, off a typo. An unparseable *version* is only a miss — cache directories
// are user-created and a stray name should be skipped, not fatal.
func satisfies(ver string, constraint manifest.Constraint) (bool, error) {
	// An unknown version satisfies nothing, checked before the empty-spec case:
	// a flavor-only constraint ("[ee]" with no range) otherwise accepted an
	// undetermined version and stamped an empty bundled_*_version into the lock.
	if strings.TrimSpace(ver) == "" {
		return false, nil
	}

	spec := strings.TrimSpace(constraint.Version)
	if spec == "" {
		return true, nil
	}

	constraints, err := version.NewConstraint(spec)
	if err != nil {
		return false, stateErrorf(
			"%w: %q (expected a comma-separated range like %q)",
			errBadConstraint, spec, ">=3.0.0,<4.0.0")
	}

	parsed, err := version.NewVersion(coreVersion(ver))
	if err != nil {
		// Cache directories are user-created; a stray name that is not a version
		// is skipped rather than failing the whole pack.
		return false, nil //nolint:nilerr // An unparseable version is a miss.
	}

	return constraints.Check(parsed), nil
}

// coreVersion reduces a reported runtime version to its major.minor.patch core,
// dropping any prerelease or build-metadata suffix.
//
// This is deliberately looser than semver's own rule, which excludes a
// prerelease from a range that does not itself name one. That rule exists for
// resolving dependencies, where an unreleased version is a hazard. Here the
// version describes a binary the user already has installed, and every
// Tarantool development build reports one - "3.8.0-entrypoint-49-g97a3b38040".
// Under the strict rule such a build satisfies no ordinary constraint at all,
// so [platform].tarantool = ">=3.0.0,<4.0.0" would reject a perfectly usable
// 3.8.0. Matching on the core version treats that build as the 3.8.0 it is;
// bundleRuntime warns separately when what it bundles is a prerelease.
func coreVersion(ver string) string {
	ver = normalizeVersion(ver)

	// Cut build metadata first, then the prerelease: "3.8.0-rc1+deadbeef".
	ver, _, _ = strings.Cut(ver, "+")
	ver, _, _ = strings.Cut(ver, "-")

	return ver
}

// isPrerelease reports whether a reported version carries a prerelease or
// build-metadata suffix, i.e. whether coreVersion had to drop anything.
func isPrerelease(ver string) bool {
	return coreVersion(ver) != normalizeVersion(ver)
}

// normalizeVersion strips a leading "v" and surrounding whitespace, leaving the
// version otherwise intact - the prerelease suffix is meaningful and is only
// dropped for constraint checking, by coreVersion.
func normalizeVersion(ver string) string {
	return strings.TrimPrefix(strings.TrimSpace(ver), "v")
}

// placeRuntime copies one resolved component into <stage>/_runtime/<name>/.
func placeRuntime(stageDir string, src runtimeSource) error {
	dst := filepath.Join(stageDir, runtimeDirName, src.Name)

	if src.Dir != "" {
		if err := copyTree(src.Dir, dst); err != nil {
			return fmt.Errorf("bundling %s: %w", src.Name, err)
		}
	} else if err := placeBinaryRuntime(dst, src); err != nil {
		return err
	}

	if src.Name == runtimeTarantool {
		return checkTarantoolLicense(dst, src)
	}

	return nil
}

// placeBinaryRuntime bundles a component resolved to a single executable, the
// fallback shape. For Tarantool the interpreter alone is not enough: its Lua
// modules live in <prefix>/share/tarantool, and a bundle missing them fails at
// require time rather than at pack time, so they come along when present.
func placeBinaryRuntime(dst string, src runtimeSource) error {
	target := filepath.Join(dst, "bin", src.Name)
	if err := copyFile(src.Binary, target); err != nil {
		return fmt.Errorf("bundling %s: %w", src.Name, err)
	}

	if src.Name != runtimeTarantool {
		return nil
	}

	prefix := filepath.Dir(filepath.Dir(src.Binary))

	share := filepath.Join(prefix, "share", "tarantool")
	if _, err := os.Stat(share); err != nil {
		return nil //nolint:nilerr // No share tree to bundle.
	}

	if err := copyTree(share, filepath.Join(dst, "share", "tarantool")); err != nil {
		return fmt.Errorf("bundling Tarantool share/: %w", err)
	}

	return nil
}

// checkTarantoolLicense enforces that a bundled Tarantool ships its LICENSE.
// A cache entry is expected to carry one; the single-binary fallback cannot, so
// the license is looked up next to the binary's install prefix.
func checkTarantoolLicense(dst string, src runtimeSource) error {
	for _, name := range tarantoolLicenseNames {
		if _, err := os.Stat(filepath.Join(dst, name)); err == nil {
			return nil
		}
	}

	if src.Dir != "" {
		return stateErrorf("%w: none of %s found in %s",
			errNoTarantoolLicense, strings.Join(tarantoolLicenseNames, ", "), src.Dir)
	}

	// Fallback path: look beside the binary, i.e. <prefix>/bin/tarantool =>
	// <prefix>/ and <prefix>/share/tarantool/.
	prefix := filepath.Dir(filepath.Dir(src.Binary))
	candidates := []string{prefix, filepath.Join(prefix, "share", "tarantool")}

	for _, dir := range candidates {
		for _, name := range tarantoolLicenseNames {
			path := filepath.Join(dir, name)
			if _, err := os.Stat(path); err == nil {
				return copyFile(path, filepath.Join(dst, "LICENSE"))
			}
		}
	}

	return stateErrorf("%w: looked beside %s in %s", errNoTarantoolLicense,
		src.Binary, strings.Join(candidates, ", "))
}
