package tarantool

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	goversion "github.com/hashicorp/go-version"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

// versionPlaceholder stands for the version in the install hint when no
// single version can be named.
const versionPlaceholder = "<version>"

// resolveInstalled chooses the tarantool for an application installed into
// the user or system scope rooted at root.
func resolveInstalled(app App, root string) (Executable, error) {
	platform, err := readPlatform(app.Dir)
	if err != nil {
		return Executable{}, err
	}

	lock, found, err := readLock(app.Dir)
	if err != nil {
		return Executable{}, err
	}

	// An installed version is unpacked from an archive, and every archive
	// carries its lock; without one there is no telling what the
	// application was packed with.
	if !found {
		return Executable{}, fmt.Errorf("cannot read %s: %w", lockPath(app.Dir), fs.ErrNotExist)
	}

	programs := acceptedPrograms(platform.EffectiveFlavor())

	if lock.BundledTarantool != "" {
		return fromLock(app, root, programs, lock.BundledTarantool)
	}

	return fromConstraint(app, root, programs, platform)
}

// readPlatform reads [platform].tarantool from the manifest in dir.
func readPlatform(dir string) (manifest.Constraint, error) {
	path := manifestPath(dir)

	data, err := os.ReadFile(path)
	if err != nil {
		return manifest.Constraint{Version: "", Flavor: ""},
			fmt.Errorf("cannot read application manifest: %w", err)
	}

	// The warnings are about fields this tt does not know; the command that
	// loads the application reports them, and none of them is the platform.
	parsed, _, err := manifest.ParseManifest(data)
	if err != nil {
		return manifest.Constraint{Version: "", Flavor: ""},
			fmt.Errorf("cannot parse %s: %w", path, err)
	}

	return parsed.Platform.Tarantool, nil
}

// readLock reads the lock in dir; found is false when there is none.
func readLock(dir string) (*manifest.Lock, bool, error) {
	path := lockPath(dir)

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("cannot read application lock: %w", err)
	}

	lock, err := manifest.ParseLock(data)
	if err != nil {
		return nil, false, fmt.Errorf("cannot parse %s: %w", path, err)
	}

	return lock, true, nil
}

// acceptedPrograms lists the products an application of the flavor may run
// on, in the order they are searched. Enterprise is a superset of Community,
// so a [ce] application runs on either and prefers Community, while an [ee]
// one runs on Enterprise only. tt package pack chooses the tarantool it
// bundles by the same rule, so the product it bundled is among these.
func acceptedPrograms(flavor string) []binary.Program {
	if flavor == flavorEE {
		return []binary.Program{binary.ProgramEe}
	}

	return []binary.Program{binary.ProgramCe, binary.ProgramEe}
}

// fromLock finds the version the lock bundles among the installations of the
// accepted programs.
//
// The version is looked up exactly: it is the tarantool the application was
// packed and tested with, and a near version is a different runtime. Which
// other version would do is for the user to say, by installing it.
func fromLock(
	app App, root string, programs []binary.Program, bundled string,
) (Executable, error) {
	for _, program := range programs {
		for _, selected := range installationIDs(program, bundled) {
			path, ok := installedExecutable(root, program, selected)
			if ok {
				return Executable{
					Path:     path,
					Origin:   OriginLock,
					Program:  program,
					Version:  selected,
					Warnings: nil,
				}, nil
			}
		}
	}

	return Executable{}, fmt.Errorf(
		"%w in the %s scope: %s in %s bundles %s %s, which is not under %s; "+
			"install it with: %s",
		ErrNotInstalled, app.Scope, lockFileName, app.Dir, programs[0], bundled,
		filepath.Join(root, binary.VersionsDirName),
		installHint(programs[0], bundled, app.Scope))
}

// installationIDs lists the identities under which tt binary may have
// installed version of program, most exact first.
//
// A Community version is installed under its own name. An Enterprise one may
// also be a component of the SDK archive of that version, and be installed as
// the standalone identity tt binary derives from the archive version
// (binary.BundleInstallationID). The standalone identity comes last: it keeps
// the release and drops the archive revision, so it is the same Tarantool but
// not necessarily the same build.
func installationIDs(program binary.Program, ver string) []string {
	ids := []string{ver}

	if program != binary.ProgramEe {
		return ids
	}

	ids = append(ids, binary.SDKVersionPrefix+ver)

	parsed, err := version.Parse(ver)
	if err == nil {
		standalone := binary.BundleInstallationID(program, parsed, false)
		if standalone != ver {
			ids = append(ids, standalone)
		}
	}

	return ids
}

// installedExecutable returns the executable of the installation of program
// identified by id in root, and whether it is there. An installation counts
// when its executable is a regular file, the way tt binary decides it: a
// version directory an interrupted installation left without one does not.
func installedExecutable(root string, program binary.Program, id string) (string, bool) {
	path := filepath.Join(binary.InstallationPath(root, program, id), program.Exec())

	return path, binary.IsRegularFile(path)
}

// fromConstraint chooses the highest installed version that satisfies
// [platform].tarantool, searching the accepted programs in order.
func fromConstraint(
	app App, root string, programs []binary.Program, platform manifest.Constraint,
) (Executable, error) {
	constraints, err := parseConstraint(platform.Version)
	if err != nil {
		return Executable{}, fmt.Errorf("cannot use [platform].tarantool of %s: %w",
			manifestPath(app.Dir), err)
	}

	for _, program := range programs {
		best, found, err := highestSatisfying(root, program, constraints)
		if err != nil {
			return Executable{}, err
		}

		if !found {
			continue
		}

		path, _ := installedExecutable(root, program, best.id)

		var warnings []string

		// A development build satisfies the constraint by its core version
		// (see satisfies); say so, rather than let it run a deployment
		// unremarked.
		if best.version.Release.Type != version.TypeRelease {
			warnings = append(warnings, fmt.Sprintf(
				"%s %s, chosen for %q of [platform].tarantool, is a prerelease build",
				program, best.id, platform.Version))
		}

		return Executable{
			Path:     path,
			Origin:   OriginConstraint,
			Program:  program,
			Version:  best.id,
			Warnings: warnings,
		}, nil
	}

	return Executable{}, fmt.Errorf(
		"%w in the %s scope: %s in %s bundles no tarantool, and nothing under %s "+
			"satisfies %q of [platform].tarantool; install a version that does with: %s",
		ErrNotInstalled, app.Scope, lockFileName, app.Dir,
		filepath.Join(root, binary.VersionsDirName), platform.String(),
		installHint(programs[0], versionPlaceholder, app.Scope))
}

// parseConstraint parses the version part of [platform].tarantool. An empty
// one constrains nothing and yields nil.
//
// It is parsed the way tt package pack parses it to choose the tarantool it
// bundles, so an application packed --without-deps runs on a version pack
// would have bundled for it. A constraint that does not parse is an error
// rather than a miss: running some other tarantool off a typo is worse than
// not running.
func parseConstraint(spec string) (goversion.Constraints, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}

	constraints, err := goversion.NewConstraint(spec)
	if err != nil {
		return nil, fmt.Errorf("invalid version constraint %q: %w", spec, err)
	}

	return constraints, nil
}

// candidate is an installation considered for a constraint.
type candidate struct {
	// id identifies the installation, as binary.InstalledVersions lists it.
	id string
	// version is id parsed.
	version version.Version
}

// highestSatisfying returns the highest installation of program in root whose
// version satisfies constraints. found is false when none does.
//
// Installations named by something other than a version, such as master or a
// commit, cannot be compared with a constraint without running them, and are
// skipped. Of two installations of the same version, as a standalone
// Enterprise build and an SDK component may be, the one whose name sorts first
// is taken, so the choice does not depend on the order of directory entries.
func highestSatisfying(
	root string, program binary.Program, constraints goversion.Constraints,
) (candidate, bool, error) {
	ids, err := binary.InstalledVersions(root, program)
	if err != nil {
		return candidate{}, false, err
	}

	var (
		best  candidate
		found bool
	)

	for _, selected := range ids {
		_, installed := installedExecutable(root, program, selected)
		if !installed {
			continue
		}

		parsed, err := version.Parse(strings.TrimPrefix(selected, binary.SDKVersionPrefix))
		if err != nil || !satisfies(parsed, constraints) {
			continue
		}

		next := candidate{id: selected, version: parsed}
		if !found || higher(next, best) {
			best = next
			found = true
		}
	}

	return best, found, nil
}

// higher reports whether left is to be chosen over right.
func higher(left, right candidate) bool {
	if version.IsLess(right.version, left.version) {
		return true
	}

	if version.IsLess(left.version, right.version) {
		return false
	}

	return left.id < right.id
}

// satisfies reports whether ver meets constraints; nil constraints accept any
// version.
//
// The version is compared by its major.minor.patch core, as tt package pack
// compares it: every development build of Tarantool carries a prerelease
// suffix, and under the strict semver rule it would satisfy no ordinary range
// at all.
func satisfies(ver version.Version, constraints goversion.Constraints) bool {
	if constraints == nil {
		return true
	}

	core, err := goversion.NewVersion(fmt.Sprintf("%d.%d.%d", ver.Major, ver.Minor, ver.Patch))
	if err != nil {
		return false
	}

	return constraints.Check(core)
}
