package inventory

import (
	"strings"

	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// The user and system scopes hold applications rather than guests of a shared
// tree: every installed version is an install root of its own under
// <root>/versions/<app>/, and <root>/apps/<app> selects the active one (see
// cli/manifest/state). List reads what is installed there and uninstall
// removes an application with all its versions.

// listApps reports the applications installed in the user or system scope,
// one entry per application with its active version and every installed one.
//
// The rock index of --tree is refused here: it answers which rocks the
// packages of one tree share, and the versions of these scopes share none —
// each carries its own .rocks/.
func listApps(scope state.Scope, opts ListOptions) (*Listing, error) {
	if opts.Rocks {
		return nil, stateErrorf("%w: in the %s scope every installed version has rocks "+
			"of its own and shares none", ErrTreeScope, scope)
	}

	root, err := state.ScopeRoot(scope, opts.ProjectDir)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	apps, err := state.InstalledApps(root)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	listing := &Listing{
		Scope:    scope,
		Root:     root,
		Packages: make([]Entry, 0, len(apps)),
		Rocks:    nil,
	}

	for _, app := range apps {
		listing.Packages = append(listing.Packages, appEntry(root, app))
	}

	return listing, nil
}

// appEntry projects an installed application onto its listing entry. The
// description and the dependencies are those of the active version, or of the
// highest installed one when none is active.
func appEntry(root string, app state.App) Entry {
	described := app.Active
	if described == "" {
		described = app.Versions[len(app.Versions)-1]
	}

	entry := Entry{
		Name: app.Name, Version: app.Active, Versions: app.Versions, Primary: false,
		Description: "", Dependencies: nil, Direct: nil,
	}

	pkg, ok := state.ReadPrimary(state.VersionDir(root, app.Name, described))
	if !ok {
		return entry
	}

	read := entryOf(pkg)

	entry.Description = read.Description
	entry.Dependencies = read.Dependencies
	entry.Direct = read.Direct

	return entry
}

// uninstallApp removes an application from the user or system scope: every
// installed version and the link selecting the active one.
//
// It refuses while an instance of the application runs, as its pid file under
// <root>/var/run/<app>/ tells: the instance runs from one of the versions, and
// removing it pulls the code from under the instance. It removes nothing but
// the application: the runtimes in versions/ may serve other applications,
// and the instance data under <root>/var/ and the host files under
// <root>/etc/<app>/ are kept (decision 16). Removing one version alone is not
// a command yet.
func uninstallApp(opts UninstallOptions, scope state.Scope) (*UninstallResult, error) {
	err := state.CheckAppName(opts.Package)
	if err != nil {
		return nil, stateErrorf("%w; tt binary uninstall removes the products of tt binary", err)
	}

	root, err := state.ScopeRoot(scope, opts.ProjectDir)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	app, err := state.ReadApp(root, opts.Package)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	if !app.OnDisk {
		return nil, stateErrorf("%w: %q in %s scope", ErrNotInstalled, opts.Package, scope)
	}

	err = state.CheckNotRunning(root, app.Name)
	if err != nil {
		return nil, stateErrorf("cannot uninstall %s: %w", app.Name, err)
	}

	if !opts.Yes && opts.Confirm != nil {
		prompt := "remove " + app.Name
		if len(app.Versions) > 0 {
			prompt += " with its installed versions " + strings.Join(app.Versions, ", ")
		}

		if !opts.Confirm(prompt + "?") {
			return nil, stateErrorf("%w", ErrAborted)
		}
	}

	err = state.RemoveApp(root, app.Name)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	return &UninstallResult{
		Package:             app.Name,
		Version:             app.Active,
		Versions:            app.Versions,
		Scope:               scope,
		RemovedDependencies: nil,
		KeptDependencies:    nil,
	}, nil
}
