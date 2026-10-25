package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/internal/scope"
)

// appsDirName is the directory under a user or system root holding a link
// to the active version of every installed application.
const appsDirName = "apps"

// location is where an application is: everything Load finds before it
// reads the pair.
type location struct {
	// name is the name of the application.
	name string
	// scope is the scope it was found in.
	scope Scope
	// root is the root of that scope.
	root string
	// dir is the application directory.
	dir string
}

// locate finds the application name in the scope opts selects, starting the
// project search in start, which is absolute. An empty name is the project
// the command runs in.
//
// --scope wins. Without it a name is the project the command runs in when
// that project is called so, and the system scope otherwise; the user scope
// is reached only through --scope. The other scopes are never searched for a
// name the chosen one lacks: what a command acts on must not depend on what
// happens to be installed where.
func locate(name string, opts Options, start string) (location, error) {
	if opts.Dir != "" {
		return locatePinned(name, opts)
	}

	switch opts.Scope {
	case ScopeUser, ScopeSystem:
		if name == "" {
			return location{}, failf("%w: --scope %s needs a target; "+
				"use <app>:<instance> or <app>:*", ErrInvalidTarget, opts.Scope)
		}

		return locateInstalled(name, opts.Scope)
	case ScopeProject:
		return locateProject(name, start, opts.Integrity)
	case "":
		return locateAuto(name, start, opts.Integrity)
	default:
		return location{}, failf("%w %q", ErrUnknownScope, opts.Scope)
	}
}

// locateAuto applies the rules for a command without --scope.
//
// The project's name is read even when it then turns out not to be the
// target: a project whose manifest does not parse cannot tell whether it is
// the application meant, and guessing that it is not would start an
// application from the system scope in its place.
func locateAuto(name, start string, integ integrity.IntegrityCtx) (location, error) {
	projectDir, found, err := scope.FindProject(start)
	if err != nil {
		return location{}, failf("%w", err)
	}

	if name == "" {
		if !found {
			return location{}, noProjectError(start, "; use <app>:<instance> or <app>:*")
		}

		return projectLocation(projectDir, integ)
	}

	var project location

	if found {
		project, err = projectLocation(projectDir, integ)
		if err != nil {
			return location{}, err
		}

		if project.name == name {
			return project, nil
		}
	}

	loc, err := locateInstalled(name, ScopeSystem)
	if err == nil {
		return loc, nil
	}

	if !errors.Is(err, ErrAppNotFound) {
		return location{}, err
	}

	if found {
		return location{}, fmt.Errorf("%w; the project in %s is called %q",
			err, project.dir, project.name)
	}

	return location{}, withTT2Hint(err, start)
}

// locateProject finds the project the command runs in, for --scope project.
// A name, when given, must be the project's: the target names an
// application, and the project is not a stand-in for any of them.
func locateProject(name, start string, integ integrity.IntegrityCtx) (location, error) {
	projectDir, found, err := scope.FindProject(start)
	if err != nil {
		return location{}, failf("%w", err)
	}

	if !found {
		return location{}, noProjectError(start, "")
	}

	project, err := projectLocation(projectDir, integ)
	if err != nil {
		return location{}, err
	}

	if name != "" && project.name != name {
		return location{}, failf("%w: the project in %s is called %q, not %q",
			ErrAppNotFound, project.dir, project.name, name)
	}

	return project, nil
}

// locatePinned returns the location of the application name in the directory
// opts.Dir of the scope opts.Scope, without looking for it.
//
// The watchdog of an instance comes back here to restart it after it exits:
// the restart is the same run, so it stays on the version the instance
// started from, which <root>/apps/<app> may no longer point to, and on the
// project it started in, wherever the command that started it ran. Looking
// the application up again would follow the link to whatever version is
// active by then. The directory is therefore taken as it is: a project is
// not searched for upward from it, and a version is not checked against the
// link. The name must still be the application's, and the scope root is
// found as usual, so the pair of the host and the runtime artifacts stay
// where they were.
func locatePinned(name string, opts Options) (location, error) {
	dir := opts.Dir
	if !filepath.IsAbs(dir) {
		return location{}, failf("the application directory %q %w", dir, errNotAbsolute)
	}

	info, err := os.Stat(dir)
	if isAbsent(err) {
		return location{}, failf("%w: the application directory %s is gone", ErrAppNotFound, dir)
	}

	if err != nil {
		return location{}, failf("cannot reach the application directory: %w", err)
	}

	if !info.IsDir() {
		return location{}, failf("%w: %s is not a directory", ErrAppNotFound, dir)
	}

	switch opts.Scope {
	case ScopeProject:
		project, err := projectLocation(dir, opts.Integrity)
		if err != nil {
			return location{}, err
		}

		if name != "" && project.name != name {
			return location{}, failf("%w: the project in %s is called %q, not %q",
				ErrAppNotFound, dir, project.name, name)
		}

		return project, nil
	case ScopeUser, ScopeSystem:
		if name == "" {
			return location{}, failf("%w: --scope %s needs a target; "+
				"use <app>:<instance> or <app>:*", ErrInvalidTarget, opts.Scope)
		}

		root, err := scopeRoot(opts.Scope)
		if err != nil {
			return location{}, failf("%w", err)
		}

		return location{name: name, scope: opts.Scope, root: root, dir: dir}, nil
	case "":
		return location{}, failf("the application directory %s %w", dir, errPinnedWithoutScope)
	default:
		return location{}, failf("%w %q", ErrUnknownScope, opts.Scope)
	}
}

// noProjectError reports that start is in no project, with hint appended, and
// says so when start is in a tt2 environment.
func noProjectError(start, hint string) error {
	err := failf("%w: no %s or %s/*/%s in %s or its parents%s", ErrNoProject,
		scope.ManifestFileName, scope.TemplatesDirName, scope.InstancesFileName, start, hint)

	return withTT2Hint(err, start)
}

// projectLocation returns the location of the project in projectDir. The
// application directory is the project directory, the instances' working
// directory; the scope root is .tarantool in it.
func projectLocation(projectDir string, integ integrity.IntegrityCtx) (location, error) {
	name, err := projectName(projectDir, integ)
	if err != nil {
		return location{}, err
	}

	return location{
		name:  name,
		scope: ScopeProject,
		root:  scope.ProjectRoot(projectDir),
		dir:   projectDir,
	}, nil
}

// projectName returns the name of the project in projectDir: [package].name
// of its manifest, or the name of the directory when it has none.
//
// The manifest is where the project names itself, and the name it gives is
// what tt package install names the installed application, so the two
// scopes agree on it. A manifest that is there and does not parse, or names
// nothing a target can address, is an error: the project marker counts by
// existence alone, so this is the first place that reads it. The rest of the
// manifest is not validated: a constraint the project has not got right yet
// stops tt package build, not the instances.
func projectName(projectDir string, integ integrity.IntegrityCtx) (string, error) {
	manifestPath := filepath.Join(projectDir, scope.ManifestFileName)

	found, err := exists(manifestPath)
	if err != nil {
		return "", failf("%w", err)
	}

	if !found {
		name := filepath.Base(projectDir)
		if !validName(name) {
			return "", failf("%w: cannot name the project in %s after its directory",
				ErrInvalidManifest, projectDir)
		}

		return name, nil
	}

	data, err := readFile(integ, manifestPath)
	if err != nil {
		return "", failf("%w: %w", ErrInvalidManifest, err)
	}

	man, _, err := manifest.ParseManifest(data)
	if err != nil {
		return "", failf("%w %s: %w", ErrInvalidManifest, manifestPath, err)
	}

	name := man.Package.Name
	if !manifest.ValidPackageName(name) {
		return "", failf("%w %s: package.name %q must match [a-z][a-z0-9-]* "+
			"and not be reserved", ErrInvalidManifest, manifestPath, name)
	}

	return name, nil
}

// locateInstalled finds the application name in the user or system scope:
// <root>/apps/<name>.
//
// <root>/apps/<name> is a link to the active version, and it is resolved
// here, once: the version directory itself becomes the application
// directory. Everything the instances get, their working directory and the
// paths of the description among them, then points into that version, and an
// instance keeps running the version it started from when the link is
// switched to another.
func locateInstalled(name string, scp Scope) (location, error) {
	root, err := scopeRoot(scp)
	if err != nil {
		return location{}, failf("%w", err)
	}

	link := filepath.Join(root, appsDirName, name)

	dir, err := filepath.EvalSymlinks(link)
	if isAbsent(err) {
		return location{}, failf("%w: %q is not installed in the %s scope (no %s); "+
			"pass --scope to look in another scope", ErrAppNotFound, name, scp, link)
	}

	if err != nil {
		return location{}, failf("cannot resolve application %q: %w", name, err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		return location{}, failf("cannot resolve application %q: %w", name, err)
	}

	if !info.IsDir() {
		return location{}, failf("%w: %s is not a directory", ErrAppNotFound, link)
	}

	return location{name: name, scope: scp, root: root, dir: dir}, nil
}

// scopeRoot returns the root of the user or the system scope.
func scopeRoot(scp Scope) (string, error) {
	if scp == ScopeUser {
		root, err := scope.UserRoot()
		if err != nil {
			return "", fmt.Errorf("cannot find the user scope: %w", err)
		}

		return root, nil
	}

	root, err := scope.SystemRoot()
	if err != nil {
		return "", fmt.Errorf("cannot find the system scope: %w", err)
	}

	return root, nil
}
