package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	goversion "github.com/hashicorp/go-version"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/pidfile"
	ttscope "github.com/tarantool/tt/v3/internal/scope"
)

// Directories of the versions layout. versions/ and the naming of the
// directories in it are tt binary's; apps/ and var/ are added next to them.
const (
	// VersionsDirName holds the installed versions of every product and
	// application, versions/<name>/<name>_<ver>/.
	VersionsDirName = binary.VersionsDirName
	// AppsDirName holds one link per installed application, apps/<app>,
	// selecting its active version.
	AppsDirName = "apps"
	// varDirName, runDirName and PidFileName locate the pid file of a running
	// instance, <root>/var/run/<app>/<inst>/tt.pid.
	varDirName = "var"
	runDirName = "run"
	// PidFileName is the pid file an instance of an application leaves in its
	// run directory.
	PidFileName = "tt.pid"
)

var (
	// ErrProductName reports an application named like a product tt binary
	// installs into the same versions/ directory.
	ErrProductName = errors.New("the name belongs to a tt binary product")
	// ErrBadVersion reports a package version that cannot name a directory.
	ErrBadVersion = errors.New("invalid package version")
	// ErrInstancesRunning reports an application whose files cannot be
	// removed or replaced because instances of it run.
	ErrInstancesRunning = errors.New("the application has running instances")
)

// ScopeRoot returns the root of the scope: the directory whose versions/
// holds the runtimes installed for it, and, in the user and system scopes,
// the applications as well.
//
// The roots are the ones tt binary and tt instance use, taken from
// internal/scope, so an application installed here runs on a tarantool
// tt binary installed and is found by tt instance. The project root is
// <projectDir>/.tarantool, counted from the install root itself: tt package
// install acts on the directory it is given and does not search upwards, and
// the project tarantool tt run picks is looked up in the same place.
func ScopeRoot(scope Scope, projectDir string) (string, error) {
	var (
		root string
		err  error
	)

	switch scope {
	case ScopeProject:
		return ttscope.ProjectRoot(projectDir), nil
	case ScopeUser:
		root, err = ttscope.UserRoot()
	case ScopeSystem:
		root, err = ttscope.SystemRoot()
	default:
		return "", fmt.Errorf("%w %q", ErrUnknownScope, scope)
	}

	if err != nil {
		return "", fmt.Errorf("locating the %s scope root: %w", scope, err)
	}

	return root, nil
}

// CheckAppName refuses a package name that cannot be an application of the
// user or system scope. Applications live in versions/ next to the products
// tt binary installs there, so one named like a product would put its
// versions into the directory of that product, and tt binary list would read
// them as installations of it.
func CheckAppName(name string) error {
	if binary.IsProductName(name) {
		return fmt.Errorf("%w: %q; an application cannot be installed under that name "+
			"in the user or system scope", ErrProductName, name)
	}

	return nil
}

// CheckVersion refuses a version that cannot name the directory of an
// installed version: an empty one, and one that is not a single path element.
// A version comes from the VERSION file of an archive, and the archive is
// input, so it is checked before any path is built from it.
func CheckVersion(ver string) error {
	if ver == "" || ver == "." || ver == ".." ||
		strings.ContainsAny(ver, `/\`) || strings.ContainsRune(ver, 0) {
		return fmt.Errorf("%w %q: it must name a directory", ErrBadVersion, ver)
	}

	return nil
}

// VersionName returns the directory name of version ver of the application
// name, <name>_<ver>: the convention tt binary names its installations by, so
// that the two kinds of installation read the same in versions/.
func VersionName(name, ver string) string {
	return name + version.FsSeparator + ver
}

// AppDir returns the directory holding every installed version of the
// application name, <root>/versions/<name>.
func AppDir(root, name string) string {
	return filepath.Join(root, VersionsDirName, name)
}

// VersionDir returns the install root of version ver of the application name,
// <root>/versions/<name>/<name>_<ver>. It is laid out like a project: the
// manifest, the lock and VERSION at its top, .rocks/ and .templates/ inside.
func VersionDir(root, name, ver string) string {
	return filepath.Join(AppDir(root, name), VersionName(name, ver))
}

// ActiveLink returns the link selecting the active version of the
// application name, <root>/apps/<name>.
func ActiveLink(root, name string) string {
	return filepath.Join(root, AppsDirName, name)
}

// activeTarget is what ActiveLink points at for version ver. It is relative,
// ../versions/<name>/<name>_<ver>, so the root can be moved or mounted
// elsewhere without breaking the links in it.
func activeTarget(name, ver string) string {
	return filepath.Join("..", VersionsDirName, name, VersionName(name, ver))
}

// Activate makes version ver the active version of the application name by
// pointing ActiveLink at it. The link is replaced in one rename, so whoever
// resolves it at the same time gets either the previous version or this one.
func Activate(root, name, ver string) error {
	link := ActiveLink(root, name)

	err := os.MkdirAll(filepath.Dir(link), metaDirPerm)
	if err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(link), err)
	}

	err = binary.ReplaceLink(link, activeTarget(name, ver))
	if err != nil {
		return fmt.Errorf("activating %s %s: %w", name, ver, err)
	}

	return nil
}

// App is an application installed in the user or system scope.
type App struct {
	// Name is the application name, the name of its directories in versions/
	// and apps/.
	Name string
	// Versions are the installed versions, lowest first. A version counts as
	// installed when its directory holds a manifest: install writes the
	// directory under another name and renames it into place complete, so a
	// directory without one is debris, not an installation.
	Versions []string
	// Active is the version ActiveLink selects, empty when it selects none of
	// Versions.
	Active string
	// OnDisk reports whether anything of the application is on disk, its
	// directory in versions/ or its link in apps/, even with no version
	// installed: what an uninstall still has to remove.
	OnDisk bool
}

// ReadApp reads what is installed of the application name in the scope
// rooted at root. An application that is not installed is the zero App with
// the name set; only a directory that cannot be read is an error.
func ReadApp(root, name string) (App, error) {
	app := App{Name: name, Versions: nil, Active: "", OnDisk: false}

	entries, err := os.ReadDir(AppDir(root, name))

	switch {
	case err == nil:
		app.OnDisk = true
	case !errors.Is(err, fs.ErrNotExist):
		return App{}, fmt.Errorf("reading installed versions of %s: %w", name, err)
	}

	for _, entry := range entries {
		ver, ok := strings.CutPrefix(entry.Name(), name+version.FsSeparator)
		if !entry.IsDir() || !ok || CheckVersion(ver) != nil {
			continue
		}

		if isRegularFile(filepath.Join(AppDir(root, name), entry.Name(), PrimaryManifestFile)) {
			app.Versions = append(app.Versions, ver)
		}
	}

	slices.SortStableFunc(app.Versions, CompareVersions)

	link := ActiveLink(root, name)

	_, err = os.Lstat(link)
	if err == nil {
		app.OnDisk = true
	}

	// Anything at the link but a link to one of the installed versions, in the
	// relative form Activate writes or as an absolute path, selects none.
	target, err := os.Readlink(link)
	if err == nil {
		target = filepath.Clean(target)

		for _, ver := range app.Versions {
			if target == activeTarget(name, ver) || target == VersionDir(root, name, ver) {
				app.Active = ver
			}
		}
	}

	return app, nil
}

// InstalledApps lists the applications installed in the scope rooted at root,
// in name order. A directory of versions/ is an application when it is not
// the directory of a tt binary product, its name is a valid package name and
// at least one version is installed in it.
func InstalledApps(root string) ([]App, error) {
	entries, err := os.ReadDir(filepath.Join(root, VersionsDirName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filepath.Join(root, VersionsDirName), err)
	}

	var apps []App

	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || binary.IsProductName(name) || !manifest.ValidPackageName(name) {
			continue
		}

		app, err := ReadApp(root, name)
		if err != nil {
			return nil, err
		}

		if len(app.Versions) > 0 {
			apps = append(apps, app)
		}
	}

	return apps, nil
}

// RunningInstance is an instance of an application whose pid file names a
// live process.
type RunningInstance struct {
	// Name is the instance name, the name of its run directory.
	Name string
	// Pid is the process the pid file names.
	Pid int
}

// RunningInstances lists the instances of the application name that run from
// the scope rooted at root, in name order.
//
// An instance leaves its pid file at <root>/var/run/<app>/<inst>/tt.pid, under
// the root rather than in the version it runs, so that its runtime files
// outlive a version switch. Which version a running instance was started
// from is not recorded there, so a running instance counts against every
// version of its application.
func RunningInstances(root, name string) ([]RunningInstance, error) {
	runDir := filepath.Join(root, varDirName, runDirName, name)

	entries, err := os.ReadDir(runDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", runDir, err)
	}

	var running []RunningInstance

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, live, err := pidfile.Alive(filepath.Join(runDir, entry.Name(), PidFileName))
		if err != nil {
			return nil, fmt.Errorf("checking instance %s of %s: %w", entry.Name(), name, err)
		}

		if live {
			running = append(running, RunningInstance{Name: entry.Name(), Pid: pid})
		}
	}

	return running, nil
}

// CheckNotRunning refuses to go on while instances of the application name
// run from the scope rooted at root, naming them. Removing or replacing the
// files of a version under a running instance pulls its code and its rocks
// from under it, and it fails at the next require rather than now.
func CheckNotRunning(root, name string) error {
	running, err := RunningInstances(root, name)
	if err != nil {
		return err
	}

	if len(running) == 0 {
		return nil
	}

	described := make([]string, 0, len(running))
	for _, inst := range running {
		described = append(described, fmt.Sprintf("%s (pid %d)", inst.Name, inst.Pid))
	}

	return fmt.Errorf("%w: %s: %s; stop them first", ErrInstancesRunning, name,
		strings.Join(described, ", "))
}

// CompareVersions orders two application versions, for listing them and for
// deciding what --upgrade replaces. It returns a negative number when left
// is lower, a positive one when it is higher and zero when they are equal.
//
// An application version is the SemVer string tt package pack writes into
// VERSION, build metadata included, so it is compared as SemVer: the LuaRocks
// version parser the dependency code uses refuses build metadata. A version
// that does not parse sorts below every one that does, and versions that do
// not parse, or are equal as SemVer, are ordered as strings, so the order
// never depends on the order of directory entries.
func CompareVersions(left, right string) int {
	parsedLeft, errLeft := goversion.NewSemver(left)
	parsedRight, errRight := goversion.NewSemver(right)

	switch {
	case errLeft != nil && errRight == nil:
		return -1
	case errLeft == nil && errRight != nil:
		return 1
	case errLeft == nil && errRight == nil:
		if cmp := parsedLeft.Compare(parsedRight); cmp != 0 {
			return cmp
		}
	}

	return strings.Compare(left, right)
}

// isRegularFile reports whether path is a regular file.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}
