package scope

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// VarDirName is the directory of an installed version that a signed install
// links the layout of its instances from.
//
// Tarantool Enterprise starts with an empty environment when it checks
// integrity, so the TT_*_DEFAULT variables tt passes the layout through do
// not reach it, and the paths of an instance fall back to the defaults of
// Tarantool: var/lib/<inst> for the data, var/run/<inst> for the pid file
// and the console socket, var/log/<inst> for a log file, under the working
// directory, which is the version. A signed version therefore holds var/lib,
// var/run and var/log as links to <root>/var/lib/<app>, <root>/var/run/<app>
// and <root>/var/log/<app>: the defaults of Tarantool then name the files
// of the layout tt finds the instance by, and every version of the
// application leads to the same data.
const VarDirName = "var"

// ErrVarLink is wrapped by the error for an entry of var/ in a version that
// is not the link VarLinks says it is.
var ErrVarLink = errors.New("is not the link to the layout of the instances")

// VarLink is a link of an installed version to a directory of the layout.
type VarLink struct {
	// Path is the link, <version>/var/lib.
	Path string
	// Target is the directory it leads to, <root>/var/lib/<app>.
	Target string
}

// VarLinks returns the links the version in versionDir of the application
// app, installed under root, has to the directories of the layout of its
// instances, var/lib, var/run and var/log, in that order.
func VarLinks(root, app, versionDir string) []VarLink {
	links := make([]VarLink, 0, len(layoutDirs))

	for _, dir := range layoutDirs {
		links = append(links, VarLink{
			Path:   filepath.Join(versionDir, filepath.FromSlash(dir)),
			Target: filepath.Join(root, filepath.FromSlash(dir), app),
		})
	}

	return links
}

// layoutDirs are the directories of a scope root the links of a version
// lead into, relative to the root as they are relative to the version.
var layoutDirs = [...]string{DataDirName, RunDirName, LogDirName}

// LinkVar creates the links of VarLinks that the version in versionDir does
// not have yet, with var/ itself of mode perm, and checks those it has. A
// link is relative, like the link to the active version, so that the root
// may be moved or mounted elsewhere. Its target need not exist: tt creates
// the directories of an instance before the instance starts.
func LinkVar(root, app, versionDir string, perm fs.FileMode) error {
	err := os.MkdirAll(filepath.Join(versionDir, VarDirName), perm)
	if err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Join(versionDir, VarDirName), err)
	}

	for _, link := range VarLinks(root, app, versionDir) {
		present, err := checkVarLink(link)
		if err != nil {
			return err
		}

		if present {
			continue
		}

		target, err := relativeTarget(link)
		if err != nil {
			return err
		}

		err = os.Symlink(target, link.Path)
		if err != nil {
			return fmt.Errorf("linking %s to %s: %w", link.Path, link.Target, err)
		}
	}

	return nil
}

// CheckVarLinks checks the links of VarLinks the version in versionDir has,
// and reports whether any of them is missing. An entry that is not a link,
// or a link that leads elsewhere, is an error wrapping ErrVarLink.
func CheckVarLinks(root, app, versionDir string) (bool, error) {
	missing := false

	for _, link := range VarLinks(root, app, versionDir) {
		present, err := checkVarLink(link)
		if err != nil {
			return false, err
		}

		missing = missing || !present
	}

	return missing, nil
}

// checkVarLink reports whether link is in place: false when nothing is at
// its path, true when a link there leads to its target, and an error for
// anything else.
func checkVarLink(link VarLink) (bool, error) {
	info, err := os.Lstat(link.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("checking %s: %w", link.Path, err)
	}

	if info.Mode()&fs.ModeSymlink == 0 {
		return false, fmt.Errorf("%s %w %s: it is a %s", link.Path, ErrVarLink, link.Target,
			describeMode(info.Mode()))
	}

	raw, err := os.Readlink(link.Path)
	if err != nil {
		return false, fmt.Errorf("reading the link %s: %w", link.Path, err)
	}

	target := raw

	// A relative target is resolved from the directory of the link as the
	// kernel resolves it: from the path that directory resolves to, which
	// .. then climbs out of.
	if !filepath.IsAbs(target) {
		dir, err := realPath(filepath.Dir(link.Path))
		if err != nil {
			return false, err
		}

		target = filepath.Join(dir, target)
	}

	got, err := realPath(target)
	if err != nil {
		return false, err
	}

	want, err := realPath(link.Target)
	if err != nil {
		return false, err
	}

	if got != want {
		return false, fmt.Errorf("%s %w %s: it leads to %s", link.Path, ErrVarLink,
			link.Target, raw)
	}

	return true, nil
}

// relativeTarget returns the target of link as a path relative to the
// directory of the link, computed on the paths the links in them resolve
// to, so that a root reached through a link of its own still matches.
func relativeTarget(link VarLink) (string, error) {
	linkDir, err := realPath(filepath.Dir(link.Path))
	if err != nil {
		return "", err
	}

	target, err := realPath(link.Target)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(linkDir, target)
	if err != nil {
		return target, nil //nolint:nilerr // No relative path: the absolute one leads there.
	}

	return rel, nil
}

// realPath returns path with the links of its longest existing prefix
// resolved, and the rest, which does not exist yet, appended as it is.
func realPath(path string) (string, error) {
	path = filepath.Clean(path)

	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}

	parent := filepath.Dir(path)
	if parent == path {
		return path, nil
	}

	resolvedParent, err := realPath(parent)
	if err != nil {
		return "", err
	}

	return filepath.Join(resolvedParent, filepath.Base(path)), nil
}

// describeMode names the kind of file mode is.
func describeMode(mode fs.FileMode) string {
	switch {
	case mode.IsDir():
		return "directory"
	case mode.IsRegular():
		return "file"
	default:
		return "special file"
	}
}
