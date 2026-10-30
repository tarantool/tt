package install

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/v3/cli/manifest/state"
	ttscope "github.com/tarantool/tt/v3/internal/scope"
)

// Names of the temporary directories a version install leaves in
// versions/<app>/ while it runs. Neither has the <app>_<ver> shape of an
// installed version, so a crash that leaves one behind does not make it look
// installed.
const (
	// stagePattern is the directory the archive is extracted into before it
	// is renamed into place.
	stagePattern = ".install-*"
	// replacedPattern holds the version a --force reinstall replaces until
	// the new one is in place.
	replacedPattern = ".replaced-*"
	// replacedName is the replaced version inside a replacedPattern directory.
	replacedName = "version"
)

// installVersion installs the archive as one version of an application in the
// user or system scope, <root>/versions/<app>/<app>_<ver>, and makes it the
// active version by pointing <root>/apps/<app> at it.
//
// Versions of an application live side by side, so installing one version
// next to another is not a collision; installing a version that is already
// there is, unless --force replaces it. Every version is an install root of
// its own: nothing is shared with the other versions, or with other
// applications, so there is no joint resolution and no dependency ledger
// here — removing a version removes its rocks with it. The one thing shared is
// the runtime a with-deps archive bundles, installed into versions/ of the
// scope like any product of tt binary; the application finds it there by the
// version its lock records.
//
// A signed install (opts.Integrity) also gives the version var/lib,
// var/run and var/log as links to the directories of the layout of its
// instances under the root (ttscope.LinkVar): Tarantool Enterprise starts
// with an empty environment when it checks integrity, and finds the layout
// tt keeps its instances in only by its own defaults, which name those
// directories under the version. The links are not part of what was
// signed; the package itself cannot hold var/ (pack refuses it).
//
// Install activates what it installed. There is no command yet to switch
// between installed versions or to roll back to an earlier one; until there
// is, installing a version is how it is made active.
func installVersion(
	ctx context.Context, opts Options, archive *Archive, header *Header,
) (*OneResult, error) {
	name := header.Manifest.Package.Name
	ver := header.Version

	result := &OneResult{
		Archive: archive.Path(), Package: name, Version: ver, Scope: opts.Scope, Skipped: false,
	}

	err := state.CheckAppName(name)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	err = state.CheckVersion(ver)
	if err != nil {
		return nil, stateErrorf("%w (from %s of %s)", err, versionFileName, archive.Path())
	}

	root, err := state.ScopeRoot(opts.Scope, opts.ProjectDir)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	app, err := state.ReadApp(root, name)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	replace, skip, err := checkVersionCollision(opts, header, root, app)
	if err != nil {
		return nil, err
	}

	if skip {
		result.Skipped = true

		return result, nil
	}

	if opts.Integrity != nil && !header.WithDeps {
		return nil, stateErrorf("%w", errSignedWithoutDeps)
	}

	// The runtime is shared, so it goes first and stays even if the version
	// then fails to install: it is an installation like any tt binary makes.
	_, err = placeRuntimes(opts, archive, header, root)
	if err != nil {
		return nil, err
	}

	err = placeVersion(ctx, opts, archive, header, root, replace)
	if err != nil {
		return nil, err
	}

	err = state.Activate(root, name, ver)
	if err != nil {
		return nil, fmt.Errorf("%s %s is installed in %s, but it is not active: %w",
			name, ver, state.VersionDir(root, name, ver), err)
	}

	return result, nil
}

// checkVersionCollision applies --upgrade and --force to the version being
// installed. It reports whether an installed version is to be replaced and
// whether the install is a no-op.
//
// --upgrade installs only a version higher than every installed one: with
// several versions side by side, "newer than what is installed" can only mean
// newer than all of them. --force replaces the version of the same name, and
// is refused while instances of the application run: their files would be
// pulled from under them. Without either, the same version again is an error.
func checkVersionCollision(
	opts Options, header *Header, root string, app state.App,
) (bool, bool, error) {
	name := header.Manifest.Package.Name
	ver := header.Version

	if opts.Upgrade && !opts.Force && len(app.Versions) > 0 {
		highest := app.Versions[len(app.Versions)-1]
		if state.CompareVersions(ver, highest) <= 0 {
			opts.warn(fmt.Sprintf("%s %s is not newer than the installed %s; nothing to do",
				name, ver, highest))

			return false, true, nil
		}
	}

	dir := state.VersionDir(root, name, ver)

	_, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}

	if err != nil {
		return false, false, fmt.Errorf("checking %s: %w", dir, err)
	}

	if !opts.Force {
		return false, false, stateErrorf("%w: %q %s in %s (use --force to reinstall it)",
			errNameCollision, name, ver, dir)
	}

	err = state.CheckNotRunning(root, name)
	if err != nil {
		return false, false, stateErrorf("cannot reinstall %s %s: %w", name, ver, err)
	}

	return true, false, nil
}

// placeVersion writes the version directory: the archive is extracted into a
// staging directory next to it, renamed into place complete, and then any
// dependency the archive does not carry is fetched from the registry into it.
//
// The rename is what makes an interrupted install harmless: until it happens
// there is no <app>_<ver> directory, so no half-written version is ever seen
// as installed. Registry rocks are installed after it, into the final path,
// because LuaRocks may write the tree path into what it deploys. If fetching
// fails, the new version is removed and the one it replaced, if any, is put
// back.
func placeVersion(
	ctx context.Context, opts Options, archive *Archive, header *Header,
	root string, replace bool,
) error {
	appDir := state.AppDir(root, header.Manifest.Package.Name)

	err := mkdirWrapped(appDir)
	if err != nil {
		return err
	}

	err = swapVersion(ctx, opts, archive, header, root, replace)
	if err != nil {
		// A first install that fails leaves no empty versions/<app>/ behind;
		// Remove fails, harmlessly, while anything else is in it.
		_ = os.Remove(appDir)

		return err
	}

	return nil
}

// swapVersion does the work of placeVersion once versions/<app>/ exists.
func swapVersion(
	ctx context.Context, opts Options, archive *Archive, header *Header,
	root string, replace bool,
) error {
	name := header.Manifest.Package.Name
	appDir := state.AppDir(root, name)
	final := state.VersionDir(root, name, header.Version)

	productName, err := selectProduct(header.Manifest)
	if err != nil {
		return err
	}

	stage, err := os.MkdirTemp(appDir, stagePattern)
	if err != nil {
		return fmt.Errorf("creating a staging directory in %s: %w", appDir, err)
	}

	// After the rename there is nothing left to remove here.
	defer func() { _ = os.RemoveAll(stage) }()

	// MkdirTemp makes the stage 0700, and the stage becomes the version
	// directory. The version gets the mode of every other directory of the
	// installation, as tt binary gives its own: in the system scope the
	// instances run as another user than the one that installs, the user
	// of the units of tt service systemd, and read the version.
	err = os.Chmod(stage, dirPerm)
	if err != nil {
		return fmt.Errorf("setting the mode of %s: %w", stage, err)
	}

	// The tree belongs to this version alone and starts empty, so the plan
	// reconciles against nothing: it only says which rocks the archive
	// carries and which the registry has to supply.
	plan, err := planDeps(header, productName, nil, state.ProjectLayout(stage), header.WithDeps)
	if err != nil {
		return err
	}

	err = archive.Extract(stage, versionMapper())
	if err != nil {
		return fmt.Errorf("extracting %s: %w", archive.Path(), err)
	}

	// The version is checked as it is to be installed, before it is: a
	// signed install puts nothing in place that fails the check.
	if opts.Integrity != nil {
		err = opts.Integrity.VerifyTree(stage)
		if err != nil {
			return stateErrorf("%s %s of %s: integrity check failed: %w", name,
				header.Version, archive.Path(), err)
		}
	}

	replaced := ""

	if replace {
		replaced, err = setAside(appDir, final)
		if err != nil {
			return err
		}
	}

	err = os.Rename(stage, final)
	if err != nil {
		return errors.Join(fmt.Errorf("installing %s: %w", final, err), putBack(replaced, final))
	}

	err = refetch(ctx, opts, state.ProjectLayout(final), plan)
	if err != nil {
		return errors.Join(err, removeAll(final), putBack(replaced, final))
	}

	// A signed version links the layout of its instances, which Tarantool
	// Enterprise only finds by its defaults when it checks integrity.
	if opts.Integrity != nil {
		err = ttscope.LinkVar(root, name, final, dirPerm)
		if err != nil {
			return errors.Join(fmt.Errorf("linking the layout of %s %s: %w", name,
				header.Version, err), removeAll(final), putBack(replaced, final))
		}
	}

	if replaced != "" {
		err = os.RemoveAll(replaced)
		if err != nil {
			opts.warn(fmt.Sprintf("cannot remove the replaced version at %s: %s", replaced, err))
		}
	}

	return nil
}

// setAside moves the installed version at dir into a temporary directory in
// appDir and returns that directory, for putBack to restore it or for the
// caller to remove it once the new version is in place.
func setAside(appDir, dir string) (string, error) {
	holder, err := os.MkdirTemp(appDir, replacedPattern)
	if err != nil {
		return "", fmt.Errorf("creating a directory in %s: %w", appDir, err)
	}

	err = os.Rename(dir, filepath.Join(holder, replacedName))
	if err != nil {
		return "", errors.Join(fmt.Errorf("moving %s aside: %w", dir, err), removeAll(holder))
	}

	return holder, nil
}

// putBack restores a version setAside moved away; an empty holder restores
// nothing.
func putBack(holder, dir string) error {
	if holder == "" {
		return nil
	}

	err := os.Rename(filepath.Join(holder, replacedName), dir)
	if err != nil {
		return fmt.Errorf("restoring the replaced version to %s: %w", dir, err)
	}

	return removeAll(holder)
}

// removeAll removes path and everything in it, wrapping the error.
func removeAll(path string) error {
	err := os.RemoveAll(path)
	if err != nil {
		return fmt.Errorf("removing %s: %w", path, err)
	}

	return nil
}
