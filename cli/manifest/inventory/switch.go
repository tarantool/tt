package inventory

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/state"
	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/internal/binary"
)

// SwitchOptions selects the version tt package switch makes active.
type SwitchOptions struct {
	// ProjectDir is the directory the command runs in. Switch refuses the
	// project scope, so it only names the project in that refusal.
	ProjectDir string
	// Scope is the scope of the application, user or system. The zero value
	// ("") is project, which switch refuses.
	Scope state.Scope
	// Package is the name of the application.
	Package string
	// Version is the installed version to make active.
	Version string
	// Warn receives non-fatal diagnostics; nil drops them.
	Warn func(string)
}

func (o SwitchOptions) warn(msg string) {
	if o.Warn != nil {
		o.Warn(msg)
	}
}

// RunningInstance is an instance of an application of the user or system
// scope that runs. It runs the version it was started from, which it tells
// when its watchdog recorded it, until it is restarted, whichever version is
// made active meanwhile.
type RunningInstance = state.RunningInstance

// SwitchResult reports what tt package switch did.
type SwitchResult struct {
	// Package is the application.
	Package string
	// Scope is its scope.
	Scope state.Scope
	// Version is the version now active.
	Version string
	// Previous is the version that was active before, empty when none was.
	Previous string
	// AlreadyActive is set when Version was active already and nothing
	// changed.
	AlreadyActive bool
	// Running are the instances of the application that run, in name order:
	// they run what they were started from until they are restarted.
	Running []RunningInstance
}

// RunningInstances lists the instances of the application name of the user or
// system scope that run, in name order, with the version each runs: for
// tt package install and switch to tell which instances do not run the active
// version yet.
func RunningInstances(scope state.Scope, projectDir, name string) ([]RunningInstance, error) {
	root, err := state.ScopeRoot(scope, projectDir)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	running, err := state.RunningInstances(root, name)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	return running, nil
}

// InstalledApp reads what is installed of the application name in the user or
// system scope opts selects, for tt package switch to offer a choice: the
// installed versions, lowest first, and the active one. An application with
// no version installed is an error, as it is for Switch.
func InstalledApp(opts SwitchOptions) (state.App, error) {
	scope, root, err := switchRoot(opts)
	if err != nil {
		return state.App{}, err
	}

	return installedApp(scope, root, opts.Package)
}

// installedApp reads the application name installed in the scope rooted at
// root, refusing one with no version installed.
func installedApp(scope state.Scope, root, name string) (state.App, error) {
	app, err := state.ReadApp(root, name)
	if err != nil {
		return state.App{}, stateErrorf("%w", err)
	}

	if len(app.Versions) == 0 {
		return state.App{}, stateErrorf("%w: %q in %s scope (no versions in %s)",
			ErrNotInstalled, name, scope, state.AppDir(root, name))
	}

	return app, nil
}

// Switch makes an installed version of an application of the user or system
// scope the active one: it points <root>/apps/<app> at
// <root>/versions/<app>/<app>_<ver>/, the way tt package install activates the
// version it installs (state.Activate). The link is replaced in one rename, so
// whatever resolves it at the same time gets either version, never none.
//
// Nothing is stopped or restarted. An instance resolves the link when it is
// started and runs from the version it found for its whole life, restarts
// after a failure included, so the instances that run keep running the version
// they were started from until tt instance restart, or systemctl restart for
// the instances under the units of tt service systemd. The units name the
// application, not a version: they run tt instance start <app>:<inst>, which
// resolves the link anew. Switch reports the instances that run, with the
// version each runs, so that the caller can tell to restart them.
//
// A version is switched to only if it can run: the tarantool its lock bundles
// must still be installed in the scope, as tt instance start looks it up
// (tarantool.Resolve), since nothing keeps tt binary uninstall from removing
// it. A version packed --without-deps bundles none and runs on whatever
// installed tarantool satisfies its [platform].tarantool, which tt binary
// changes; finding none is a warning only.
//
// The project scope holds one version of every package and has no versions/
// to switch between: switch refuses it, pointing at tt package install
// --force.
func Switch(opts SwitchOptions) (*SwitchResult, error) {
	err := state.CheckVersion(opts.Version)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	scope, root, err := switchRoot(opts)
	if err != nil {
		return nil, err
	}

	app, err := installedApp(scope, root, opts.Package)
	if err != nil {
		return nil, err
	}

	if !slices.Contains(app.Versions, opts.Version) {
		return nil, stateErrorf("%w: %s %s in %s scope; installed versions: %s",
			ErrVersionNotInstalled, app.Name, opts.Version, scope, DescribeVersions(app))
	}

	result := &SwitchResult{
		Package: app.Name, Scope: scope, Version: opts.Version, Previous: app.Active,
		AlreadyActive: app.Active == opts.Version, Running: nil,
	}

	if !result.AlreadyActive {
		err = checkRunnable(opts, scope, root, app.Name)
		if err != nil {
			return nil, err
		}

		err = state.Activate(root, app.Name, opts.Version)
		if err != nil {
			return nil, stateErrorf("%w", err)
		}
	}

	// The switch is done; an instance that cannot be told is not a reason
	// to report it failed. The instances are told for an active version too:
	// some may not run it yet.
	result.Running, err = state.RunningInstances(root, app.Name)
	if err != nil {
		opts.warn(fmt.Sprintf("cannot tell which instances of %s run: %s", app.Name, err))
	}

	return result, nil
}

// DescribeVersions lists the installed versions of app, lowest first, the
// active one marked: "1.0.0, 1.1.0 (active)".
func DescribeVersions(app state.App) string {
	described := make([]string, 0, len(app.Versions))

	for _, ver := range app.Versions {
		if ver == app.Active {
			ver += " (active)"
		}

		described = append(described, ver)
	}

	return strings.Join(described, ", ")
}

// switchRoot checks the scope and the name of the application switch is
// asked for, and returns the scope and its root.
func switchRoot(opts SwitchOptions) (state.Scope, string, error) {
	scope, err := parseScope(opts.Scope)
	if err != nil {
		return "", "", err
	}

	if scope == state.ScopeProject {
		return "", "", stateErrorf("%w: a project holds one version of every package, "+
			"with no versions/ to switch between; install the version wanted with "+
			"tt package install --force <archive>, or pass --scope user or --scope system "+
			"for an application installed there", ErrSwitchScope)
	}

	// The name becomes a path.
	if !manifest.ValidPackageName(opts.Package) {
		return "", "", stateErrorf("%w: %q", ErrBadPackageName, opts.Package)
	}

	err = state.CheckAppName(opts.Package)
	if err != nil {
		return "", "", stateErrorf("%w; tt binary switch switches the products of tt binary",
			err)
	}

	root, err := state.ScopeRoot(scope, opts.ProjectDir)
	if err != nil {
		return "", "", stateErrorf("%w", err)
	}

	return scope, root, nil
}

// checkRunnable refuses to switch to a version whose bundled tarantool is no
// longer installed in the scope, by the lookup tt instance start makes.
func checkRunnable(opts SwitchOptions, scope state.Scope, root, name string) error {
	dir := state.VersionDir(root, name, opts.Version)
	lock := state.ReadLockFile(filepath.Join(dir, state.PrimaryLockFile))

	binaryScope := binary.ScopeUser
	if scope == state.ScopeSystem {
		binaryScope = binary.ScopeSystem
	}

	_, err := tarantool.Resolve(tarantool.App{Scope: binaryScope, Dir: dir})
	if err == nil {
		return nil
	}

	if lock == nil || lock.BundledTarantool == "" {
		opts.warn(fmt.Sprintf("%s %s: %s", name, opts.Version, err))

		return nil
	}

	return stateErrorf("cannot switch %s to %s: %w: %w", name, opts.Version,
		ErrVersionCannotStart, err)
}
