package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/hashicorp/go-version"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/internal/binary"
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
// carry. The first one found beside the binary is bundled as LICENSE.
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
	// Prefix is the install prefix of Binary as the caller knows it; empty
	// when the caller has none, in which case it is guessed from Binary.
	Prefix string
	// Fallback marks a component resolved from the active tt environment
	// rather than the runtime cache, which is worth warning about.
	Fallback bool
	// Installation marks a Binary that is a tt binary installation of the
	// project: its headers, beside it under include/, come along.
	Installation bool
	// Flavor is the build's flavor, "ce" or "ee": the cache tree it was found
	// in, or what the active binary reported. Empty when undetermined.
	Flavor string
}

// RuntimeOptions carries what bundleRuntime needs to find and place runtimes.
type RuntimeOptions struct {
	// CacheDir is the runtime cache root; entries live at
	// <CacheDir>/<component>/<flavor>/<version>/.
	CacheDir string
	// ProjectRoot is the root of the project scope, <project>/.tarantool,
	// whose tt binary installations are searched when the cache has no
	// match; empty skips them.
	ProjectRoot string
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
	// ActiveTarantoolPrefix is the install prefix tt resolved for the active
	// Tarantool (TT_CLI_TARANTOOL_PREFIX or the build banner), where its
	// LICENSE and share/tarantool are looked for first. Empty means unknown,
	// and the binary's own location is searched instead.
	ActiveTarantoolPrefix string
	ActiveTt              string
	ActiveTtVersion       string
	ActiveTtFlavor        string
	ActiveTcm             string
	ActiveTcmVersion      string
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
// cache satisfying the constraint wins. Failing that, a tt binary installation
// of the project is taken: the active one when it satisfies the constraint,
// else the highest that does. Failing that, the active tt environment's own
// binary is accepted if it satisfies the constraint, with a warning: the cache
// and the installations are builds named by their version, the active binary
// is whatever the shell finds.
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
		active     activeBinary
	}{
		{runtimeTarantool, req.Platform.Tarantool, activeBinary{
			path:    req.ActiveTarantool,
			version: req.ActiveTarantoolVersion,
			flavor:  req.ActiveTarantoolFlavor,
			prefix:  req.ActiveTarantoolPrefix,
		}},
		{runtimeTt, req.Platform.Tt, activeBinary{
			path:    req.ActiveTt,
			version: req.ActiveTtVersion,
			flavor:  req.ActiveTtFlavor,
		}},
		// TCM is Enterprise-only and carries no flavor (manifest validation
		// rejects one), so its effective flavor is never compared.
		{runtimeTcm, req.Platform.Tcm, activeBinary{
			path:    req.ActiveTcm,
			version: req.ActiveTcmVersion,
		}},
	}

	var bundled BundledVersions

	for _, want := range wanted {
		if want.constraint.IsZero() {
			// Only tcm is optional; tarantool and tt are required by Validate.
			continue
		}

		src, err := resolveRuntime(req, want.name, want.constraint, want.active)
		if err != nil {
			return BundledVersions{}, err
		}

		if src.Fallback && req.Warn != nil {
			req.Warn(fmt.Sprintf(
				"no %s %s in the runtime cache (%s) or the project; bundling the active "+
					"%s %s instead (tt binary install %s puts one into the project)",
				want.name, want.constraint.Version, req.CacheDir, want.name, src.Version,
				productName(want.name, want.constraint.EffectiveFlavor())))
		}

		// A prerelease satisfies the constraint here on its core version alone
		// (see coreVersion), so say so rather than letting a development build
		// end up in a release archive unremarked.
		if isPrerelease(src.Version) && req.Warn != nil {
			req.Warn(fmt.Sprintf(
				"bundling %s %s, a prerelease build, to satisfy %q",
				want.name, src.Version, want.constraint.Version))
		}

		err = placeRuntime(stageDir, src, req.Warn)
		if err != nil {
			return BundledVersions{}, err
		}

		switch want.name {
		case runtimeTarantool:
			bundled.Tarantool = lockedTarantoolVersion(src)
		case runtimeTt:
			bundled.Tt = src.Version
		case runtimeTcm:
			bundled.Tcm = src.Version
		}
	}

	return bundled, nil
}

// releaseBuild matches the version a Community tarantool built from a release
// tag reports: the release, zero commits after the tag and the commit hash.
var releaseBuild = regexp.MustCompile(`^(\d+\.\d+\.\d+)-0-g[0-9a-f]+$`)

// lockedTarantoolVersion is the version the lock records for the bundled
// tarantool (decision 26). A Community build of a release tag,
// 3.8.1-0-ge643f88a5e0c, is recorded as the release, 3.8.1: the name
// tt binary install tarantool 3.8.1 installs it under, so tt package install
// lays the bundled runtime into versions/ under the same name instead of a
// second one, and an install hint built from the lock names a version that
// can be installed. A build that is not on a tag keeps the full version, as it
// is no release; an Enterprise version, or one of an undetermined flavor, is
// kept as reported.
func lockedTarantoolVersion(src runtimeSource) string {
	if src.Flavor != flavorCE {
		return src.Version
	}

	match := releaseBuild.FindStringSubmatch(src.Version)
	if match == nil {
		return src.Version
	}

	return match[1]
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
	// prefix is the install prefix as the caller knows it; empty for unknown.
	prefix string
}

// acceptedFlavors lists the build flavors that satisfy a requirement, in the
// order the cache is searched. Enterprise is a superset of Community, so a
// [ce] requirement is met by either build; an [ee] requirement only by an
// Enterprise one, since a CE build lacks what the manifest asked for in a way
// nothing downstream detects. The requirement's own flavor comes first so that
// a [ce] project bundles the CE tree whenever one is cached, and reaches for
// EE only when nothing else satisfies it.
func acceptedFlavors(flavor string) []string {
	if flavor == flavorCE {
		return []string{flavorCE, flavorEE}
	}

	return []string{flavor}
}

// resolveRuntime picks the source for one runtime component: cache first,
// active binary second. Both must satisfy the constraint's flavor as well as
// its version; see acceptedFlavors for what satisfies a flavor.
func resolveRuntime(
	req RuntimeOptions, name string, constraint manifest.Constraint,
	active activeBinary,
) (runtimeSource, error) {
	flavor := constraint.EffectiveFlavor()
	accepted := acceptedFlavors(flavor)

	for _, tree := range accepted {
		dir, ver, ok, err := findInCache(req.CacheDir, name, tree, constraint)
		if err != nil {
			return runtimeSource{}, err
		}

		if ok {
			return runtimeSource{Name: name, Version: ver, Dir: dir, Flavor: tree}, nil
		}
	}

	src, ok, err := findInstalled(req.ProjectRoot, name, constraint, accepted)
	if err != nil || ok {
		return src, err
	}

	versionOK, err := activeVersionOK(active, constraint)
	if err != nil {
		return runtimeSource{}, err
	}

	if versionOK && activeFlavorOK(active, flavor) {
		return runtimeSource{
			Name:     name,
			Version:  normalizeVersion(active.version),
			Binary:   active.path,
			Prefix:   active.prefix,
			Fallback: true,
			Flavor:   active.flavor,
		}, nil
	}

	// The version matched and only the flavor did not: say so, because the
	// generic "does not satisfy" reads as a version mismatch, and the fix is a
	// different one - the manifest's [flavor], not the cache.
	if versionOK {
		return runtimeSource{}, stateErrorf(
			"%w: no %s satisfying %s found in the runtime cache %s or the project, "+
				"and the active %s (%s) matches the version but not the flavor: %s "+
				"requires %s; %s, or change [platform].%s",
			errNoRuntime, name, constraint.String(), req.CacheDir, name,
			describeActive(active), "["+flavor+"]", describeFlavors(accepted),
			runtimeRemedy(req.CacheDir, name, flavor), name)
	}

	// No binary at all, a tt with no tarantool say: there is no version to
	// fall short of, and saying that one does not satisfy it reads as if one
	// had been looked at.
	if active.path == "" {
		return runtimeSource{}, stateErrorf(
			"%w: no %s satisfying %s found in the runtime cache %s or the project, "+
				"and tt found no %s to bundle instead; %s",
			errNoRuntime, name, constraint.String(), req.CacheDir, name,
			runtimeRemedy(req.CacheDir, name, flavor))
	}

	return runtimeSource{}, stateErrorf(
		"%w: no %s satisfying %s found in the runtime cache %s or the project, "+
			"and the active %s (%s) does not satisfy it; %s",
		errNoRuntime, name, constraint.String(), req.CacheDir, name,
		describeActive(active), runtimeRemedy(req.CacheDir, name, flavor))
}

// runtimeRemedy says how to give pack a runtime it found none of: install one
// into the project with tt binary install, or place a build in the cache.
func runtimeRemedy(cacheDir, name, flavor string) string {
	return fmt.Sprintf("install one with tt binary install %s, or place a matching build "+
		"under %s", productName(name, flavor), filepath.Join(cacheDir, name, flavor, "<version>"))
}

// productName is the tt binary product a component of the flavor is installed
// as: tarantool-ee for an [ee] tarantool, say.
func productName(name, flavor string) string {
	programs := installedPrograms(name, []string{flavor})
	if len(programs) == 0 {
		return name
	}

	return programs[0].String()
}

// installedPrograms lists the tt binary products a component of the given
// accepted flavors is installed as, in the order of the flavors. TCM has a
// single product whatever the flavor.
func installedPrograms(name string, flavors []string) []binary.Program {
	byFlavor := map[string]map[string]binary.Program{
		runtimeTarantool: {flavorCE: binary.ProgramCe, flavorEE: binary.ProgramEe},
		runtimeTt:        {flavorCE: binary.ProgramTt, flavorEE: binary.ProgramTtEe},
		runtimeTcm:       {flavorCE: binary.ProgramTcm, flavorEE: binary.ProgramTcm},
	}

	var programs []binary.Program

	for _, flavor := range flavors {
		program, ok := byFlavor[name][flavor]
		if ok && !slices.Contains(programs, program) {
			programs = append(programs, program)
		}
	}

	return programs
}

// installedFlavor is the flavor of a component installed as program: the
// Enterprise products are ee, the others ce, and TCM, which has none, is
// left undetermined.
func installedFlavor(program binary.Program) string {
	switch program {
	case binary.ProgramEe, binary.ProgramTtEe:
		return flavorEE
	case binary.ProgramCe, binary.ProgramTt:
		return flavorCE
	default:
		return ""
	}
}

// findInstalled looks for the component among the tt binary installations of
// the project scope rooted at root, <root>/versions/<product>/: the one the
// active link <root>/bin/<executable> points to when it satisfies the
// constraint, else the highest that does. Only standalone installations named
// by a version are considered: an SDK component, or a build named by a commit,
// has no version to check without running it, and the name the lock would
// record for it is not one tt package install can lay a runtime under.
func findInstalled(
	root, name string, constraint manifest.Constraint, flavors []string,
) (runtimeSource, bool, error) {
	if root == "" {
		return runtimeSource{}, false, nil
	}

	programs := installedPrograms(name, flavors)

	program, active, ok := activeInstallation(root, programs)
	if ok {
		matched, err := installationSatisfies(root, program, active, constraint)
		if err != nil {
			return runtimeSource{}, false, err
		}

		if matched {
			return installedSource(root, name, program, active), true, nil
		}
	}

	for _, program := range programs {
		installed, err := binary.InstalledVersions(root, program)
		if err != nil {
			return runtimeSource{}, false, err
		}

		var matches []string

		for _, selected := range installed {
			matched, err := installationSatisfies(root, program, selected, constraint)
			if err != nil {
				return runtimeSource{}, false, err
			}

			if matched {
				matches = append(matches, selected)
			}
		}

		if len(matches) > 0 {
			sortVersionsDesc(matches)

			return installedSource(root, name, program, matches[0]), true, nil
		}
	}

	return runtimeSource{}, false, nil
}

// activeInstallation decodes the active link of the scope rooted at root for
// the executable the programs share into the installation it points to, when
// that is an installation of one of them in the scope.
func activeInstallation(root string, programs []binary.Program) (binary.Program, string, bool) {
	if len(programs) == 0 {
		return binary.ProgramUnknown, "", false
	}

	target, err := filepath.EvalSymlinks(filepath.Join(root, binary.BinDirName,
		programs[0].Exec()))
	if err != nil {
		return binary.ProgramUnknown, "", false
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return binary.ProgramUnknown, "", false
	}

	for _, program := range programs {
		productDir := filepath.Join(realRoot, binary.VersionsDirName, program.String())
		installation := filepath.Dir(target)

		if filepath.Dir(installation) != productDir || filepath.Base(target) != program.Exec() {
			continue
		}

		selected, found := binary.InstallationVersion(program, filepath.Base(installation))
		if found {
			return program, selected, true
		}
	}

	return binary.ProgramUnknown, "", false
}

// installationSatisfies reports whether the installation selected of program
// in root is a standalone one, holds its executable and is of a version that
// satisfies the constraint.
func installationSatisfies(
	root string, program binary.Program, selected string, constraint manifest.Constraint,
) (bool, error) {
	if strings.HasPrefix(selected, binary.SDKVersionPrefix) {
		return false, nil
	}

	exe := filepath.Join(binary.InstallationPath(root, program, selected), program.Exec())
	if !binary.IsRegularFile(exe) {
		return false, nil
	}

	return satisfies(selected, constraint)
}

// installedSource describes the installation selected of program in root as
// the source of the component. Its version is the name of the installation,
// which tt package install lays the bundled runtime under, so the two agree.
func installedSource(root, name string, program binary.Program, selected string) runtimeSource {
	dir := binary.InstallationPath(root, program, selected)

	return runtimeSource{
		Name:         name,
		Version:      selected,
		Binary:       filepath.Join(dir, program.Exec()),
		Prefix:       dir,
		Installation: true,
		Flavor:       installedFlavor(program),
	}
}

// activeVersionOK reports whether the active binary exists and its version
// satisfies the constraint.
func activeVersionOK(active activeBinary, constraint manifest.Constraint) (bool, error) {
	if active.path == "" {
		return false, nil
	}

	return satisfies(active.version, constraint)
}

// activeFlavorOK reports whether the active binary's flavor satisfies the
// requirement's. An undetermined flavor is accepted only for a [ce]
// requirement: ce is the default and overwhelmingly the common case, whereas
// silently treating an unverified build as Enterprise would be a licensing
// error, not just a bug.
func activeFlavorOK(active activeBinary, flavor string) bool {
	if active.flavor == "" {
		return flavor == flavorCE
	}

	return slices.Contains(acceptedFlavors(flavor), active.flavor)
}

// describeFlavors renders the accepted flavors for an error message, e.g.
// "a [ce] or [ee] build".
func describeFlavors(flavors []string) string {
	parts := make([]string, 0, len(flavors))
	for _, f := range flavors {
		parts = append(parts, "["+f+"]")
	}

	return "a " + strings.Join(parts, " or ") + " build"
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
// constraint within one flavor. Cache entries are directories laid out as
// <cache>/<component>/<flavor>/<version>/, so a CE and an EE build of the same
// version can coexist and an [ee] requirement can never resolve to a CE tree.
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

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		ok, err := satisfies(entry.Name(), constraint)
		if err != nil {
			return "", "", false, err
		}

		if ok {
			matches = append(matches, entry.Name())
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
		left, erri := version.NewVersion(normalizeVersion(versions[i]))
		right, errj := version.NewVersion(normalizeVersion(versions[j]))

		if erri != nil || errj != nil {
			return versions[i] > versions[j]
		}

		return left.GreaterThan(right)
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
//
// A build of a release tag, 3.8.1-0-ge643f88a5e0c, is the release itself
// (decision 26, lockedTarantoolVersion): the zero commits after the tag and
// the commit git describe adds make no prerelease of it. A build that is
// commits past its tag, or one of an -rc or -beta tag, is still one.
func isPrerelease(ver string) bool {
	ver = normalizeVersion(ver)
	if releaseBuild.MatchString(ver) {
		return false
	}

	return coreVersion(ver) != ver
}

// normalizeVersion strips a leading "v" and surrounding whitespace, leaving the
// version otherwise intact - the prerelease suffix is meaningful and is only
// dropped for constraint checking, by coreVersion.
func normalizeVersion(ver string) string {
	return strings.TrimPrefix(strings.TrimSpace(ver), "v")
}

// placeRuntime copies one resolved component into <stage>/_runtime/<name>/.
func placeRuntime(stageDir string, src runtimeSource, warn func(string)) error {
	dst := filepath.Join(stageDir, runtimeDirName, src.Name)

	if src.Dir != "" {
		err := copyTree(src.Dir, dst)
		if err != nil {
			return fmt.Errorf("bundling %s: %w", src.Name, err)
		}
	} else {
		err := placeBinaryRuntime(dst, src)
		if err != nil {
			return err
		}
	}

	if src.Name == runtimeTarantool {
		return bundleTarantoolLicense(dst, src, warn)
	}

	return nil
}

// placeBinaryRuntime bundles a component resolved to a single executable, the
// fallback shape. For Tarantool the interpreter alone is not enough: its Lua
// modules live in <prefix>/share/tarantool, and a bundle missing them fails at
// require time rather than at pack time, so they come along when present. A
// tt binary installation keeps the headers of its Tarantool beside it, under
// include/tarantool/, and they come along too, for tt package install to lay
// them out as tt binary does.
func placeBinaryRuntime(dst string, src runtimeSource) error {
	target := filepath.Join(dst, "bin", src.Name)

	err := copyFile(src.Binary, target)
	if err != nil {
		return fmt.Errorf("bundling %s: %w", src.Name, err)
	}

	if src.Name != runtimeTarantool {
		return nil
	}

	if src.Installation {
		headers := filepath.Join(binary.IncDirName, runtimeTarantool)

		_, err = os.Stat(filepath.Join(src.Prefix, headers))
		if err == nil {
			err = copyTree(filepath.Join(src.Prefix, headers), filepath.Join(dst, headers))
			if err != nil {
				return fmt.Errorf("bundling Tarantool headers: %w", err)
			}
		}
	}

	for _, prefix := range binaryPrefixes(src) {
		share := filepath.Join(prefix, "share", "tarantool")

		_, err = os.Stat(share)
		if err != nil {
			continue
		}

		err = copyTree(share, filepath.Join(dst, "share", "tarantool"))
		if err != nil {
			return fmt.Errorf("bundling Tarantool share/: %w", err)
		}

		return nil
	}

	return nil
}

// binaryPrefixes lists the directories that may be the install prefix of a
// single-binary source, most trustworthy first: the prefix the caller
// resolved, then the parent of the binary's directory (the <prefix>/bin/
// layout of a packaged install), then the binary's own directory (the flat
// layout of the Enterprise SDK, where tarantool sits beside env.sh with no
// bin/ in between). Duplicates are dropped so a caller prefix that equals
// one of the guesses is not searched twice.
func binaryPrefixes(src runtimeSource) []string {
	binDir := filepath.Dir(src.Binary)
	candidates := []string{src.Prefix, filepath.Dir(binDir), binDir}

	prefixes := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if c != "" && !slices.Contains(prefixes, c) {
			prefixes = append(prefixes, c)
		}
	}

	return prefixes
}

// bundleTarantoolLicense brings a bundled Tarantool's LICENSE along. A cache
// entry is expected to carry one already; the single-binary fallback cannot, so
// the license is looked up next to the binary's install prefix and copied in.
//
// A missing license is reported and does not stop the pack: the Enterprise SDK
// ships no license file at all - not in the unpacked bundle and not in the
// tarball - so refusing here would make an EE runtime unpackable rather than
// making a license appear.
func bundleTarantoolLicense(dst string, src runtimeSource, warn func(string)) error {
	for _, name := range tarantoolLicenseNames {
		_, err := os.Stat(filepath.Join(dst, name))
		if err == nil {
			return nil
		}
	}

	if src.Dir != "" {
		if warn != nil {
			warn(fmt.Sprintf("bundling Tarantool without a license: none of %s in %s",
				strings.Join(tarantoolLicenseNames, ", "), src.Dir))
		}

		return nil
	}

	// Fallback path: look in every directory that may be the binary's install
	// prefix, and in its share/tarantool/, where a packaged install keeps it.
	var candidates []string

	for _, prefix := range binaryPrefixes(src) {
		candidates = append(candidates, prefix, filepath.Join(prefix, "share", "tarantool"))
	}

	for _, dir := range candidates {
		for _, name := range tarantoolLicenseNames {
			path := filepath.Join(dir, name)

			_, err := os.Stat(path)
			if err == nil {
				return copyFile(path, filepath.Join(dst, "LICENSE"))
			}
		}
	}

	if warn != nil {
		warn(fmt.Sprintf("bundling Tarantool without a license: looked beside %s in %s",
			src.Binary, strings.Join(candidates, ", ")))
	}

	return nil
}
