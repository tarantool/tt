package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/v3/cli/manifest/state"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

// The runtime a with-deps archive bundles is installed the way tt binary
// installs products, into versions/ of the scope root, rather than kept as a
// _runtime/ tree inside the package:
//
//   - in the user and system scopes the applications installed there share it
//     with each other and with tt binary, and the tarantool an application
//     runs on is looked up in versions/tarantool/ by the version its lock
//     bundles (cli/tarantool), not taken from a directory next to it;
//   - in the project scope it goes to <project>/.tarantool/versions/, the
//     project root tt binary installs into, and the project tarantool is
//     the one .tarantool/bin/tarantool selects.
//
// An installation is named by the version the lock records for it, so the
// name and the lock agree by construction and the resolver finds what install
// put there. That version is what pack read from the bundled binary, and it
// may be longer than the one tt binary install takes on its command line
// (3.8.1-0-ge643f88a5e0c against 3.8.1); the two are different installations
// of the same release. Versions are not normalised here: which spelling names
// an installation is not decided yet.
//
// Only an installation that does not exist yet is laid down. One that exists,
// whoever installed it, is the same version and is left alone: replacing it
// could pull the runtime from under a running application. The active links
// of the scope, bin/ and include/, are not touched in the user and system
// scopes: an application runs on the tarantool its lock names, not on the
// one tt binary switch selected for the shell.

// bundledRuntime is one component of the archive's _runtime/, as the product
// tt binary would install it as.
type bundledRuntime struct {
	// dir is the component's directory in _runtime/: tarantool, tt or tcm.
	dir string
	// program is the product it is installed as.
	program binary.Program
	// version is the version the lock records for it, and the version the
	// installation is named by.
	version string
}

// String names the component for messages.
func (b bundledRuntime) String() string {
	return b.program.String() + " " + b.version
}

// installPath is where the component is installed in the scope rooted at root.
func (b bundledRuntime) installPath(root string) string {
	return binary.InstallationPath(root, b.program, b.version)
}

// executable is the component's executable once installed in root.
func (b bundledRuntime) executable(root string) string {
	return filepath.Join(b.installPath(root), b.program.Exec())
}

// Runtime component directories inside _runtime/, as pack writes them.
const (
	runtimeTarantool = "tarantool"
	runtimeTt        = "tt"
	runtimeTcm       = "tcm"
	// runtimeBinDir is the bin/ level most bundled components have; the
	// Enterprise SDK is flat and has none.
	runtimeBinDir = "bin"
	// flavorEE is the Enterprise flavor of a [platform] constraint.
	flavorEE = "ee"
)

// bundledRuntimes lists the components the archive bundles, as the products
// tt binary installs them as. A component counts when the lock records a
// version for it; whether _runtime/ actually holds it is seen on extraction.
//
// The lock records versions only, not the edition, so the edition comes from
// the [platform] flavor the archive was packed for: an [ee] tarantool or tt
// is installed as tarantool-ee or tt-ee, anything else as the Community
// product. That is also the product the resolver searches first for such an
// application.
func bundledRuntimes(header *Header) []bundledRuntime {
	platform := header.Manifest.Platform

	tarantool := binary.ProgramCe
	if platform.Tarantool.EffectiveFlavor() == flavorEE {
		tarantool = binary.ProgramEe
	}

	tt := binary.ProgramTt
	if platform.Tt.EffectiveFlavor() == flavorEE {
		tt = binary.ProgramTtEe
	}

	all := []bundledRuntime{
		{dir: runtimeTarantool, program: tarantool, version: header.Lock.BundledTarantool},
		{dir: runtimeTt, program: tt, version: header.Lock.BundledTt},
		{dir: runtimeTcm, program: binary.ProgramTcm, version: header.Lock.BundledTcm},
	}

	var bundled []bundledRuntime

	for _, component := range all {
		if component.version != "" {
			bundled = append(bundled, component)
		}
	}

	return bundled
}

// placeRuntimes installs the runtime a with-deps archive bundles into the
// versions/ of the scope rooted at root, and returns every bundled component
// that is installed there afterwards, laid now or found in place.
//
// The components are extracted together into a staging directory in
// versions/ and each is renamed into place complete, so an interrupted
// install leaves no installation tt binary would take for a whole one.
func placeRuntimes(opts Options, archive *Archive, header *Header, root string) (
	[]bundledRuntime, error,
) {
	if !header.WithDeps {
		return nil, nil
	}

	var (
		present []bundledRuntime
		missing []bundledRuntime
	)

	for _, component := range bundledRuntimes(header) {
		if !layable(opts, component) {
			continue
		}

		exists, complete, err := runtimeInstalled(root, component)
		if err != nil {
			return nil, err
		}

		switch {
		case !exists:
			missing = append(missing, component)
		case complete:
			present = append(present, component)
		default:
			// An installation tt binary did not finish is left for tt binary
			// to repair rather than overwritten here.
			opts.warn(fmt.Sprintf("%s at %s has no %s executable; the bundled one is not "+
				"laid over it, reinstall it with tt binary install", component,
				component.installPath(root), component.program.Exec()))
		}
	}

	if len(missing) == 0 {
		return present, nil
	}

	versions := filepath.Join(root, state.VersionsDirName)

	err := mkdirWrapped(versions)
	if err != nil {
		return nil, err
	}

	stage, err := os.MkdirTemp(versions, ".runtime-*")
	if err != nil {
		return nil, fmt.Errorf("creating a staging directory in %s: %w", versions, err)
	}

	defer func() { _ = os.RemoveAll(stage) }()

	err = archive.Extract(stage, runtimeMapper(missing))
	if err != nil {
		return nil, fmt.Errorf("extracting the bundled runtime of %s: %w", archive.Path(), err)
	}

	for _, component := range missing {
		placed, err := moveRuntime(opts, stage, root, component)
		if err != nil {
			return nil, err
		}

		if placed {
			present = append(present, component)
		}
	}

	return present, nil
}

// runtimeInstalled reports whether the installation the component would be
// laid as exists, and whether it is complete: whether its executable is a
// regular file, which is how tt binary tells an installation from what an
// interrupted one left.
func runtimeInstalled(root string, component bundledRuntime) (bool, bool, error) {
	dir := component.installPath(root)

	_, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}

	if err != nil {
		return false, false, fmt.Errorf("checking %s: %w", dir, err)
	}

	return true, binary.IsRegularFile(component.executable(root)), nil
}

// layable reports whether the component's version can name an installation
// that tt binary lists. tt binary list parses the name of every version
// directory it finds and fails as a whole on one it cannot, so a version that
// does not parse is not laid down at all.
func layable(opts Options, component bundledRuntime) bool {
	_, err := version.Parse(component.version)
	if err == nil && state.CheckVersion(component.version) == nil {
		return true
	}

	opts.warn(fmt.Sprintf("the lock bundles %s %q, which cannot name a tt binary "+
		"installation; the bundled %s is not installed", component.dir, component.version,
		component.dir))

	return false
}

// runtimeMapper maps the archive's _runtime/ onto one directory per component
// in the staging directory, shaped the way tt binary installs a product: the
// executable at the top, taken from bin/ or, for the flat Enterprise SDK,
// from the top of the component; and for tarantool, the headers under
// include/tarantool/. Nothing else of _runtime/ is installed, as tt binary
// installs nothing else either.
func runtimeMapper(components []bundledRuntime) func(string) (string, bool) {
	return func(name string) (string, bool) {
		rest, ok := strings.CutPrefix(name, runtimeDirName+"/")
		if !ok {
			return "", false
		}

		dir, inside, _ := strings.Cut(rest, "/")

		for _, component := range components {
			if component.dir != dir {
				continue
			}

			exec := component.program.Exec()

			switch {
			case inside == runtimeBinDir+"/"+exec || inside == exec:
				return dir + "/" + exec, true
			case component.program.IsTarantool() &&
				strings.HasPrefix(inside, binary.IncDirName+"/"+exec+"/"):
				return dir + "/" + inside, true
			}
		}

		return "", false
	}
}

// moveRuntime renames one extracted component into its installation
// directory and reports whether it is installed there afterwards. A
// component the archive names in its lock but does not carry an executable
// for is skipped with a warning, and so is one installed by somebody else
// in the meantime.
func moveRuntime(opts Options, stage, root string, component bundledRuntime) (bool, error) {
	extracted := filepath.Join(stage, component.dir)

	if !binary.IsRegularFile(filepath.Join(extracted, component.program.Exec())) {
		opts.warn(fmt.Sprintf("the archive bundles no %s executable under %s/%s; "+
			"%s is not installed", component.program.Exec(), runtimeDirName, component.dir,
			component))

		return false, nil
	}

	dir := component.installPath(root)

	err := mkdirWrapped(filepath.Dir(dir))
	if err != nil {
		return false, err
	}

	err = os.Rename(extracted, dir)
	if err == nil {
		return true, nil
	}

	// Another install laid the same version first; it is the same runtime.
	if binary.IsRegularFile(component.executable(root)) {
		return true, nil
	}

	return false, fmt.Errorf("installing the bundled %s into %s: %w", component, dir, err)
}

// activateProjectTarantool selects the bundled tarantool in the project, when
// the project selects none yet.
//
// The project runs on .tarantool/bin/tarantool, or on the first tarantool in
// PATH when there is no such link (cli/tarantool). Selecting the bundled
// tarantool in a project that has no tarantool of its own keeps what a
// with-deps install has always meant there: the package runs on the runtime
// it was packed with. A project that already selects a tarantool chose it,
// with tt binary install or tt binary switch, and keeps it; the bundled one is
// installed next to it and a warning says how to select it.
//
// The selection is made the way tt binary switch makes it: the executable
// link, and the headers link when the runtime carries headers.
func activateProjectTarantool(opts Options, root string, present []bundledRuntime) error {
	var bundled *bundledRuntime

	for i := range present {
		if present[i].program.IsTarantool() {
			bundled = &present[i]
		}
	}

	if bundled == nil {
		return nil
	}

	link := filepath.Join(root, binary.BinDirName, bundled.program.Exec())
	executable := bundled.executable(root)

	_, err := os.Lstat(link)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return selectTarantool(root, *bundled)
	case err != nil:
		return fmt.Errorf("checking %s: %w", link, err)
	}

	selected, err := filepath.EvalSymlinks(link)
	if err == nil {
		wanted, wantedErr := filepath.EvalSymlinks(executable)
		if wantedErr == nil && selected == wanted {
			return nil
		}
	} else {
		selected = link
	}

	opts.warn(fmt.Sprintf("the project already selects %s as its tarantool; the bundled "+
		"%s is installed in %s but not selected; select it with: "+
		"tt binary switch %s %s --scope project",
		selected, bundled, bundled.installPath(root), binary.ProgramCe,
		binary.VersionArgument(bundled.program, bundled.version)))

	return nil
}

// selectTarantool makes the bundled tarantool the project's active one.
func selectTarantool(root string, bundled bundledRuntime) error {
	selection := binary.Selection{Program: bundled.program, ID: bundled.version}

	// Without headers tt binary would refuse to switch to the installation,
	// so only the executable is selected then.
	if binary.ValidateInstallation(root, selection) == nil {
		err := binary.ActivateSelections(root, []binary.Selection{selection})
		if err != nil {
			return fmt.Errorf("selecting the bundled %s in the project: %w", bundled, err)
		}

		return nil
	}

	link := filepath.Join(root, binary.BinDirName, bundled.program.Exec())

	err := mkdirWrapped(filepath.Dir(link))
	if err != nil {
		return err
	}

	err = binary.ReplaceLink(link, bundled.executable(root))
	if err != nil {
		return fmt.Errorf("selecting the bundled %s in the project: %w", bundled, err)
	}

	return nil
}
