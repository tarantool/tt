package scope

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	// ProjectDirName is the directory in the project directory that is the
	// root of the project scope.
	ProjectDirName = ".tarantool"
	// ManifestFileName is the project manifest, one of the project markers.
	ManifestFileName = "app.manifest.toml"
	// TemplatesDirName is the directory holding the project's instance
	// templates, one per subdirectory.
	TemplatesDirName = ".templates"
	// InstancesFileName is the instances description of a template; in any
	// template it is a project marker.
	InstancesFileName = "instances.yaml"
	// VersionsDirName is the directory of a user or system root that holds
	// the installed versions of binaries and applications.
	VersionsDirName = "versions"
	// AppsDirName is the directory of a user or system root that holds the
	// links to the active versions of installed applications.
	AppsDirName = "apps"
	// DataDirName, LogDirName and RunDirName are the directories of every
	// scope root that hold the runtime artifacts of the instances, one
	// subdirectory per application and instance in each, as
	// <root>/var/run/<app>/<inst>/. The layout of an instance is built on
	// them; and the run directory of an instance, which tt instance clean
	// leaves, tells that the instance has run under the root.
	DataDirName = "var/lib"
	LogDirName  = "var/log"
	RunDirName  = "var/run"
)

// ErrInstalledTree reports a directory inside the versions/ or apps/ tree of
// the user or system scope: an installed version, not a project.
var ErrInstalledTree = errors.New("is inside an installed version, not a project")

var errNotADirectory = errors.New("not a directory")

// ProjectRoot returns the root of the project scope for the project in
// projectDir.
func ProjectRoot(projectDir string) string {
	return filepath.Join(projectDir, ProjectDirName)
}

// FindProject returns the project directory for start: start itself or the
// nearest of its parents that holds a project marker, up to the filesystem
// root. found is false when none of them does. A relative start is resolved
// against the working directory; it must be an existing directory.
//
// A marker is an app.manifest.toml, or an instances.yaml in any template
// directory under .templates/: a project is described by its manifest, by
// its instance templates, or by both. config.yaml, .rocks/, .git/ and Lua
// files are not markers: they turn up in directories that are not projects.
// Neither is .tarantool/: tt binary install creates it in a directory that
// is not a project yet, and that must not capture the directories below it.
//
// A marker counts by its existence alone; its contents are not read. A
// directory whose manifest does not parse is still the project, and the
// command that reads the manifest reports the damage. Reading it here would
// either fail every command run inside a broken project, including those that
// do not need the manifest, or skip the directory and silently land in an
// enclosing project, or in none.
//
// The nearest marker wins, so a project nested in another is found from its
// own subdirectories. The path is climbed as spelled, without resolving
// symbolic links, so the parents are the ones the caller's path names.
//
// A start inside an installed version (InstalledTree) has no project, even
// though the version carries a manifest and templates like a project does:
// the project scope would put its .tarantool and the data of its instances
// inside a version that is never written to and that a switch replaces.
func FindProject(start string) (string, bool, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false, fmt.Errorf("cannot resolve project search start %q: %w", start, err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		return "", false, fmt.Errorf("cannot start project search: %w", err)
	}

	if !info.IsDir() {
		return "", false, fmt.Errorf("cannot start project search in %q: %w", dir,
			errNotADirectory)
	}

	installed, err := InstalledTree(dir)
	if err != nil || installed {
		return "", false, err
	}

	for {
		found, err := hasProjectMarker(dir)
		if err != nil {
			return "", false, err
		}

		if found {
			return dir, true, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false, nil
		}

		dir = parent
	}
}

// InstalledTree reports whether dir is inside the versions/ or apps/ tree of
// the user or system scope root, that is, in an installed version of a binary
// or an application or reached through the link to one.
//
// Both the path as spelled and the path with its links resolved are checked,
// against the root as spelled and resolved: a shell in <root>/apps/<app>
// reports the link, while a process that resolved its directory reports the
// version the link points to, and either way it is the same version.
//
// A root that cannot be placed here (no home directory, an unsupported OS)
// holds nothing tt installed, so it is skipped. A root set to a relative path
// is an error, as it is everywhere else the root is used.
func InstalledTree(dir string) (bool, error) {
	_, found, err := FindInstallation(dir)

	return found, err
}

// Installation is the scope root whose versions/ or apps/ tree a directory is
// in.
type Installation struct {
	// Root is the scope root, as UserRoot or SystemRoot returns it.
	Root string
	// User tells the user scope from the system one.
	User bool
}

// FindInstallation reports which scope root's versions/ or apps/ tree dir is
// in, by the same rules as InstalledTree. found is false outside every such
// tree. The user root is checked before the system one.
func FindInstallation(dir string) (Installation, bool, error) {
	paths, err := spellings(dir)
	if err != nil {
		return Installation{}, false, fmt.Errorf("cannot check for an installed version: %w", err)
	}

	for _, candidate := range []struct {
		rootOf func() (string, error)
		user   bool
	}{{UserRoot, true}, {SystemRoot, false}} {
		root, err := candidate.rootOf()
		if errors.Is(err, ErrRelativeRoot) {
			return Installation{}, false, err
		}

		if err != nil {
			continue
		}

		if inInstalledTree(paths, root) {
			return Installation{Root: root, User: candidate.user}, true, nil
		}
	}

	var none Installation

	return none, false, nil
}

// inInstalledTree reports whether any of paths is inside the versions/ or
// apps/ tree of root, the root taken both as spelled and with its links
// resolved.
func inInstalledTree(paths []string, root string) bool {
	roots, err := spellings(root)
	if err != nil {
		return false
	}

	for _, root := range roots {
		for _, tree := range []string{VersionsDirName, AppsDirName} {
			for _, path := range paths {
				if within(path, filepath.Join(root, tree)) {
					return true
				}
			}
		}
	}

	return false
}

// spellings returns the absolute path and, when it differs, the path with its
// links resolved. A path whose links cannot be resolved is returned as
// spelled only.
func spellings(path string) ([]string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err //nolint:wrapcheck // The caller says what it was checking.
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil || resolved == abs {
		return []string{abs}, nil //nolint:nilerr // The path as spelled still counts.
	}

	return []string{abs, resolved}, nil
}

// within reports whether path is base or inside it.
func within(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// hasProjectMarker reports whether dir holds a project marker.
func hasProjectMarker(dir string) (bool, error) {
	found, err := exists(filepath.Join(dir, ManifestFileName))
	if found || err != nil {
		return found, err
	}

	templatesDir := filepath.Join(dir, TemplatesDirName)

	templates, err := os.ReadDir(templatesDir)
	if isAbsent(err) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("cannot look for project templates: %w", err)
	}

	for _, template := range templates {
		found, err := exists(filepath.Join(templatesDir, template.Name(), InstancesFileName))
		if found || err != nil {
			return found, err
		}
	}

	return false, nil
}

// exists reports whether there is a directory entry at path, whatever its
// type. Any failure to tell, other than the entry being absent, is an error:
// taking it for absence would move the project root.
func exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}

	if isAbsent(err) {
		return false, nil
	}

	return false, fmt.Errorf("cannot look for a project marker: %w", err)
}

// isAbsent reports whether err says there is nothing at the path: either the
// entry is missing or one of the path's parents is not a directory, such as
// a regular file named .templates.
func isAbsent(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}
