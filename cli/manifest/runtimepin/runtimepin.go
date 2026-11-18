// Package runtimepin pins in the lock of a project the exact versions of the
// programs it runs on: tarantool_version, tt_version and tcm_version of
// app.manifest.lock (manifest.Lock.Tarantool, Tt and Tcm), one for each of
// [platform].tarantool, [platform].tt and, when it is set, [platform].tcm.
//
// The rocks of a lock are resolved against rock servers (cli/manifest/resolve);
// these versions come from elsewhere, from the installations of the project
// and from the sources of prebuilt archives tt binary installs from, so they
// are pinned here, by the commands that write the lock, over the lock the
// resolver made:
//
//   - tt package resolve and tt package update choose a version for every
//     program (Pin): the one the lock pins already, while it satisfies
//     [platform]; else the one installed in the project, the active one
//     first; else the one tt runs with - the tarantool it found, and tt
//     itself; else the newest release a source of prebuilt archives has. A
//     program none of them gives a version of is left unpinned with a
//     warning, as it was before there were pins; nothing fails for it.
//   - every other command that writes the lock, a build or tt package add,
//     keeps the pins that still satisfy [platform] and pins nothing new
//     (Hold), so that it neither asks a source over the network nor moves
//     a pin as a side effect.
//
// A pin is the version as tt binary names the installation of it: "3.8.1" of
// a tarantool, "3.0.0" of a tt. The product is the one the flavor of the
// constraint names, tarantool or tarantool-ee, tt or tt-ee, so that the pin
// and the product say together what to install. tt package download mirrors
// the pinned programs, tt binary install installs them and tt package pack
// bundles them.
package runtimepin

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

// Component is a program of [platform] the lock pins.
type Component string

const (
	// ComponentTarantool is [platform].tarantool, pinned as tarantool_version.
	ComponentTarantool Component = "tarantool"
	// ComponentTt is [platform].tt, pinned as tt_version.
	ComponentTt Component = "tt"
	// ComponentTcm is [platform].tcm, pinned as tcm_version.
	ComponentTcm Component = "tcm"
)

// flavorEE is the flavor of Tarantool Enterprise in a constraint.
const flavorEE = "ee"

// lockFile names the lock in messages.
const lockFile = "app.manifest.lock"

// Components lists the programs a lock pins, in the order they are pinned
// and reported.
func Components() []Component {
	return []Component{ComponentTarantool, ComponentTt, ComponentTcm}
}

// Constraint returns the [platform] constraint of the component.
func Constraint(platform manifest.Platform, component Component) manifest.Constraint {
	switch component {
	case ComponentTarantool:
		return platform.Tarantool
	case ComponentTt:
		return platform.Tt
	case ComponentTcm:
		return platform.Tcm
	default:
		return manifest.Constraint{Version: "", Flavor: ""}
	}
}

// Wanted reports whether platform asks for the component: tarantool and tt
// always, TCM when [platform].tcm is set.
func Wanted(platform manifest.Platform, component Component) bool {
	return !Constraint(platform, component).IsZero()
}

// Program returns the product the lock pins the component of platform as:
// tarantool-ee and tt-ee for the [ee] flavor, tarantool and tt for any other,
// and tcm.
func Program(platform manifest.Platform, component Component) binary.Program {
	enterprise := Constraint(platform, component).EffectiveFlavor() == flavorEE

	switch {
	case component == ComponentTarantool && enterprise:
		return binary.ProgramEe
	case component == ComponentTarantool:
		return binary.ProgramCe
	case component == ComponentTt && enterprise:
		return binary.ProgramTtEe
	case component == ComponentTt:
		return binary.ProgramTt
	case component == ComponentTcm:
		return binary.ProgramTcm
	default:
		return binary.ProgramUnknown
	}
}

// Get returns the pin of the component in lock; empty for a nil lock.
func Get(lock *manifest.Lock, component Component) string {
	if lock == nil {
		return ""
	}

	switch component {
	case ComponentTarantool:
		return lock.Tarantool
	case ComponentTt:
		return lock.Tt
	case ComponentTcm:
		return lock.Tcm
	default:
		return ""
	}
}

// set sets the pin of the component in lock.
func set(lock *manifest.Lock, component Component, pin string) {
	switch component {
	case ComponentTarantool:
		lock.Tarantool = pin
	case ComponentTt:
		lock.Tt = pin
	case ComponentTcm:
		lock.Tcm = pin
	}
}

// Satisfies reports whether pin satisfies the constraint by the rule tt
// package pack picks a runtime by (manifest.RuntimeSatisfies). A pin or a
// constraint that cannot be read satisfies nothing.
func Satisfies(pin string, constraint manifest.Constraint) bool {
	ok, err := manifest.RuntimeSatisfies(pin, constraint.Version)

	return err == nil && ok
}

// Hold sets in lock the pins of previous - the lock it replaces - that still
// satisfy [platform] of man, and leaves the others empty. It pins nothing
// new and asks nothing, which is what every command that rewrites the lock
// as a side effect wants: a build, tt package add. The returned warnings
// name the pins dropped because they no longer satisfy [platform].
func Hold(man *manifest.Manifest, previous, lock *manifest.Lock) []string {
	var warnings []string

	for _, component := range Components() {
		held, warning := hold(man.Platform, previous, component)
		set(lock, component, held)

		if warning != "" {
			warnings = append(warnings, warning)
		}
	}

	return warnings
}

// hold returns the pin of the component in previous when it still
// satisfies platform, and the warning of one it drops.
func hold(platform manifest.Platform, previous *manifest.Lock, component Component) (
	string, string,
) {
	pin := Get(previous, component)
	if pin == "" || !Wanted(platform, component) {
		return "", ""
	}

	constraint := Constraint(platform, component)
	if Satisfies(pin, constraint) {
		return pin, ""
	}

	return "", fmt.Sprintf("%s pinned %s %s, which does not satisfy %q of "+
		"[platform].%s; the pin is dropped, tt package resolve pins another one",
		lockFile, Program(platform, component), pin, constraint.Version, component)
}

// Active is a program tt runs with, a candidate of a pin.
type Active struct {
	// Version is the version the program reports: "3.8.1-0-ge643f88a5e0c" of
	// a tarantool, "3.0.0" of tt. Empty when there is none.
	Version string
	// Flavor is "ce", "ee", or empty when it is not known.
	Flavor string
}

// Releases lists the installation names of the release builds of program the
// sources of prebuilt archives have for the host, and names the source
// asked: binary.ReleaseIDs.
type Releases func(ctx context.Context, program binary.Program) ([]string, string, error)

// Options says where Pin looks for the versions it pins.
type Options struct {
	// ProjectRoot is the root of the project scope, <project>/.tarantool,
	// whose tt binary installations are looked at first; empty skips them.
	ProjectRoot string
	// Tarantool is the tarantool tt runs the project with: the active one
	// of the project, or the first on PATH.
	Tarantool Active
	// Tt is tt itself.
	Tt Active
	// Releases asks the sources of prebuilt archives; nil asks none.
	Releases Releases
}

// Pin sets in lock a pin for every program [platform] of man asks for: the
// one previous pins while it still satisfies [platform], else the one Options
// finds (see the package comment). previous is nil to choose every pin
// afresh, as a bare tt package update does. The returned warnings name the
// pins dropped and the programs left unpinned.
func Pin(ctx context.Context, man *manifest.Manifest, previous, lock *manifest.Lock,
	opts Options,
) []string {
	var warnings []string

	platform := man.Platform

	for _, component := range Components() {
		held, warning := hold(platform, previous, component)
		if held != "" || !Wanted(platform, component) {
			set(lock, component, held)

			continue
		}

		if warning != "" {
			warnings = append(warnings, warning)
		}

		pin, misses := opts.find(ctx, platform, component)
		set(lock, component, pin)

		if pin == "" {
			warnings = append(warnings, unpinned(platform, component, misses))
		}
	}

	return warnings
}

// unpinned is the warning of a program Pin found no version of.
func unpinned(platform manifest.Platform, component Component, misses []string) string {
	program := Program(platform, component)
	constraint := Constraint(platform, component)

	return fmt.Sprintf("%s pins no %s: nothing satisfies %q of [platform].%s - %s; "+
		"tt package download and tt package pack need the pin: install a version that "+
		"satisfies it into the project with tt binary install %s, then run "+
		"tt package resolve", lockFile, program, constraint.Version, component,
		strings.Join(misses, ", "), installArgument(program))
}

// installArgument is what follows tt binary install to install program.
func installArgument(program binary.Program) string {
	switch program {
	case binary.ProgramEe:
		return binary.ProgramCe.String() + " <version>ee"
	case binary.ProgramTtEe:
		return binary.ProgramTt.String() + " <version>ee"
	default:
		return program.String()
	}
}

// find looks for a version of the component that satisfies platform, in
// the order the package comment gives. It returns the version, or none and
// why each place had none.
func (opts Options) find(ctx context.Context, platform manifest.Platform,
	component Component,
) (string, []string) {
	constraint := Constraint(platform, component)
	program := Program(platform, component)

	var misses []string

	pin, err := installedPin(opts.ProjectRoot, program, constraint)

	switch {
	case err != nil:
		misses = append(misses, err.Error())
	case pin != "":
		return pin, nil
	case opts.ProjectRoot != "":
		misses = append(misses, "no installation of "+program.String()+" in "+
			filepath.Join(opts.ProjectRoot, binary.VersionsDirName, program.String()))
	}

	pin, miss := opts.activePin(program, component, constraint)
	if pin != "" {
		return pin, nil
	}

	if miss != "" {
		misses = append(misses, miss)
	}

	if opts.Releases == nil {
		return "", misses
	}

	pin, miss = releasePin(ctx, opts.Releases, program, constraint)
	if pin != "" {
		return pin, nil
	}

	return "", append(misses, miss)
}

// installedPin returns the version of the tt binary installation of program
// in the project scope rooted at root that satisfies the constraint: the
// active one, when it does, else the highest one that does. Only a
// standalone installation named by a version counts: an SDK component, or a
// build named by a commit, has no version to check without running it, and
// is not an installation tt binary install can make from the pin.
func installedPin(root string, program binary.Program, constraint manifest.Constraint) (
	string, error,
) {
	if root == "" {
		return "", nil
	}

	active, ok := activeInstallation(root, program)
	if ok && installationSatisfies(root, program, active, constraint) {
		return active, nil
	}

	installed, err := binary.InstalledVersions(root, program)
	if err != nil {
		return "", fmt.Errorf("listing the installations of %s: %w", program, err)
	}

	var candidates []string

	for _, installation := range installed {
		if installationSatisfies(root, program, installation, constraint) {
			candidates = append(candidates, installation)
		}
	}

	return newest(candidates), nil
}

// activeInstallation returns the installation of program the active link
// <root>/bin/<executable> of the scope points to, when it points to one.
func activeInstallation(root string, program binary.Program) (string, bool) {
	target, err := filepath.EvalSymlinks(filepath.Join(root, binary.BinDirName, program.Exec()))
	if err != nil {
		return "", false
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}

	installation := filepath.Dir(target)
	productDir := filepath.Join(realRoot, binary.VersionsDirName, program.String())

	if filepath.Dir(installation) != productDir || filepath.Base(target) != program.Exec() {
		return "", false
	}

	return binary.InstallationVersion(program, filepath.Base(installation))
}

// installationSatisfies reports whether the installation of program in root
// named installation is a standalone one, holds its executable and is of a
// version that satisfies the constraint.
func installationSatisfies(root string, program binary.Program, installation string,
	constraint manifest.Constraint,
) bool {
	if strings.HasPrefix(installation, binary.SDKVersionPrefix) ||
		strings.HasPrefix(installation, binary.DevVersionPrefix) {
		return false
	}

	executable := filepath.Join(binary.InstallationPath(root, program, installation),
		program.Exec())
	if !binary.IsRegularFile(executable) {
		return false
	}

	_, err := version.Parse(installation)

	return err == nil && Satisfies(installation, constraint)
}

// activePin returns the version of the program tt runs with, the tarantool
// it found or tt itself, when it is a release of the product the pin names
// and satisfies the constraint; else why not. Only Community products are
// taken so: the version an Enterprise build reports is not the name of an
// installation, and TCM is not run to ask it.
func (opts Options) activePin(program binary.Program, component Component,
	constraint manifest.Constraint,
) (string, string) {
	var active Active

	switch program {
	case binary.ProgramCe:
		active = opts.Tarantool
	case binary.ProgramTt:
		active = opts.Tt
	default:
		return "", ""
	}

	if active.Version == "" {
		return "", ""
	}

	what := "the " + string(component) + " tt runs with, " + active.Version + ","

	if active.Flavor == flavorEE {
		return "", what + " is Tarantool Enterprise"
	}

	pin, ok := releaseOf(active.Version)
	if !ok {
		return "", what + " is no release"
	}

	if !Satisfies(pin, constraint) {
		return "", what + " does not"
	}

	return pin, ""
}

// releaseOf returns the release a reported version is of, by the name tt
// binary installs it under: 3.8.1 of the 3.8.1-0-ge643f88a5e0c a Community
// tarantool built of a tag reports, 3.0.0 of tt's 3.0.0. ok is false for a
// build past a tag, which no release is.
func releaseOf(reported string) (string, bool) {
	release, ok := manifest.RuntimeRelease(reported)
	if ok {
		return release, true
	}

	reported = strings.TrimPrefix(strings.TrimSpace(reported), "v")

	parsed, err := version.Parse(reported)
	if err != nil || parsed.Additional != 0 || parsed.Hash != "" {
		return "", false
	}

	return reported, true
}

// releasePin returns the newest release of program the sources have that
// satisfies the constraint: the newest one that is no prerelease, or, while
// none is, the newest prerelease, as tt binary install takes the latest tt;
// else why none.
func releasePin(ctx context.Context, releases Releases, program binary.Program,
	constraint manifest.Constraint,
) (string, string) {
	ids, source, err := releases(ctx, program)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "", err.Error()
		}

		return "", "the sources of prebuilt archives could not answer: " + err.Error()
	}

	var stable, prerelease []string

	for _, release := range ids {
		parsed, err := version.Parse(release)
		if err != nil || !Satisfies(release, constraint) {
			continue
		}

		if parsed.Release.Type == version.TypeRelease {
			stable = append(stable, release)
		} else {
			prerelease = append(prerelease, release)
		}
	}

	if pin := newest(stable); pin != "" {
		return pin, ""
	}

	if pin := newest(prerelease); pin != "" {
		return pin, ""
	}

	return "", source + " has no release of " + program.String() + " for this platform that does"
}

// newest returns the highest of the versions, empty for none. Of two equal
// versions the one that sorts first by name is taken, so the choice does not
// depend on the order the versions were listed in.
func newest(versions []string) string {
	var (
		best       string
		bestParsed version.Version
	)

	for _, candidate := range versions {
		parsed, err := version.Parse(candidate)
		if err != nil {
			continue
		}

		switch {
		case best == "",
			binary.LessVersion(bestParsed, parsed),
			!binary.LessVersion(parsed, bestParsed) && candidate < best:
			best, bestParsed = candidate, parsed
		}
	}

	return best
}

// IsPinned reports whether the lock pins every program platform asks for.
func IsPinned(platform manifest.Platform, lock *manifest.Lock) bool {
	return !slices.ContainsFunc(Components(), func(component Component) bool {
		return Wanted(platform, component) && Get(lock, component) == ""
	})
}
