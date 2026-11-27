package cargo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/sdk/build"
)

// This file finds the Cargo.toml of a package and the root of its workspace
// from the files, as cargo finds them, for the pack of a source rock, which
// runs no cargo (checkWorkspace): the source rock holds the files of the
// project alone.

// manifestName is the manifest of a cargo package or workspace.
const manifestName = "Cargo.toml"

// checkWorkspace returns an error wrapping build.ErrOutside when the
// package cargo builds in cwd, a directory of project, has its Cargo.toml,
// or the root of its workspace - the Cargo.toml and the Cargo.lock cargo
// builds it with - out of project, as cargo finds them (findRoot): the
// source rock of the package holds the files of the project only. A cwd of
// no Cargo.toml at or above it is left to cargo, which fails there.
func checkWorkspace(project, cwd string) error {
	manifest, err := findManifest(cwd)
	if err != nil || manifest == "" {
		return err
	}

	dir := filepath.Dir(manifest)

	if !within(project, dir) {
		return fmt.Errorf("%w: the %s of the cargo package, %s, is out of the project",
			build.ErrOutside, manifestName, relTo(project, manifest))
	}

	root, err := findRoot(dir)
	if err != nil {
		return err
	}

	if within(project, root) {
		return nil
	}

	return fmt.Errorf("%w: the cargo package in %s is of the workspace of %s, whose %s and %s "+
		"cargo builds it with", build.ErrOutside, relTo(project, dir), relTo(project, root),
		manifestName, lockfileName)
}

// within reports whether dir is project or a directory in it.
func within(project, dir string) bool {
	rel, err := filepath.Rel(project, dir)

	return err == nil && filepath.IsLocal(rel)
}

// relTo is dir relative to project, as a message names it, or dir when it
// has no such path.
func relTo(project, dir string) string {
	rel, err := filepath.Rel(project, dir)
	if err != nil {
		return dir
	}

	return filepath.ToSlash(rel)
}

// findManifest returns the Cargo.toml nearest to dir, in it or above, which
// cargo runs against there, as cargo locate-project names it; empty for
// none.
func findManifest(dir string) (string, error) {
	for current := filepath.Clean(dir); ; {
		manifest := filepath.Join(current, manifestName)

		ok, err := isFile(manifest)
		if err != nil || ok {
			return manifest, err
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", nil
		}

		current = parent
	}
}

// findRoot returns the directory of the root of the workspace of the package
// whose Cargo.toml is in dir, as cargo finds it: dir, when its Cargo.toml
// has a [workspace]; the directory package.workspace names; else the
// nearest directory above dir whose Cargo.toml has a [workspace] that does
// not exclude dir; else dir, a package of no workspace.
func findRoot(dir string) (string, error) {
	own, err := readManifest(filepath.Join(dir, manifestName))
	if err != nil {
		return "", err
	}

	if own.workspace {
		return dir, nil
	}

	if own.packageWorkspace != "" {
		return filepath.Join(dir, filepath.FromSlash(own.packageWorkspace)), nil
	}

	for current := filepath.Dir(dir); ; {
		manifest := filepath.Join(current, manifestName)

		ok, err := isFile(manifest)
		if err != nil {
			return "", err
		}

		if ok {
			ancestor, err := readManifest(manifest)
			if err != nil {
				return "", err
			}

			if ancestor.workspace && !ancestor.excludes(current, dir) {
				return current, nil
			}
		}

		parent := filepath.Dir(current)
		if parent == current {
			return dir, nil
		}

		current = parent
	}
}

// isFile reports whether path is a file; not, when there is nothing there.
func isFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}

	return info.Mode().IsRegular(), nil
}

// cargoManifest is what a Cargo.toml says of the workspace it is of.
type cargoManifest struct {
	// workspace is set for a Cargo.toml of a [workspace], the root of one.
	workspace bool
	// exclude are the paths workspace.exclude leaves out of the workspace,
	// relative to its root.
	exclude []string
	// packageWorkspace is the root of the workspace package.workspace names,
	// relative to the package; empty for none.
	packageWorkspace string
}

// excludes reports whether the workspace of the Cargo.toml of root leaves
// out the package in dir, as cargo tells it: dir is in a directory of
// workspace.exclude.
func (m cargoManifest) excludes(root, dir string) bool {
	for _, path := range m.exclude {
		if within(filepath.Join(root, filepath.FromSlash(path)), dir) {
			return true
		}
	}

	return false
}

// readManifest reads what the Cargo.toml at path says of its workspace
// (parseManifest).
func readManifest(path string) (cargoManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cargoManifest{}, fmt.Errorf("reading %s: %w", path, err)
	}

	return parseManifest(string(data)), nil
}

// parseManifest reads the TOML of a Cargo.toml for what it says of its
// workspace - a table [workspace] or [workspace.<name>], or a key
// workspace.<name>, its exclude, and package.workspace - by its lines, as
// cargo manifests write them: a table a line, a key a line, an array of
// strings of exclude over the lines it takes.
func parseManifest(text string) cargoManifest {
	var (
		manifest cargoManifest
		table    string
		// array is the value of exclude so far while it spans lines.
		array     strings.Builder
		inExclude bool
	)

	for line := range strings.Lines(text) {
		line = strings.TrimSpace(stripComment(line))

		if inExclude {
			array.WriteString(line)

			if closesArray(array.String()) {
				manifest.exclude = append(manifest.exclude, tomlStrings(array.String())...)
				inExclude = false
			}

			continue
		}

		if name, ok := tableName(line); ok {
			table = name
			if table == "workspace" || strings.HasPrefix(table, "workspace.") {
				manifest.workspace = true
			}

			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.ReplaceAll(strings.TrimSpace(key), " ", "")
		if table != "" {
			key = table + "." + key
		}

		value = strings.TrimSpace(value)

		switch {
		case key == "workspace.exclude":
			manifest.workspace = true

			array.Reset()
			array.WriteString(value)

			if closesArray(value) {
				manifest.exclude = append(manifest.exclude, tomlStrings(value)...)
			} else {
				inExclude = true
			}
		case key == "workspace" || strings.HasPrefix(key, "workspace."):
			manifest.workspace = true
		case key == "package.workspace":
			if values := tomlStrings(value); len(values) == 1 {
				manifest.packageWorkspace = values[0]
			}
		}
	}

	return manifest
}

// tableName returns the name of the table line opens, [name] or [[name]],
// its dotted keys without the spaces around them.
func tableName(line string) (string, bool) {
	if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
		return "", false
	}

	name := strings.Trim(line, "[]")

	return strings.ReplaceAll(strings.TrimSpace(name), " ", ""), true
}

// stripComment returns line without its comment: from a # that is not in a
// string.
func stripComment(line string) string {
	var quote rune

	for index, char := range line {
		switch {
		case quote != 0 && char == quote:
			quote = 0
		case quote != 0:
		case char == '"' || char == '\'':
			quote = char
		case char == '#':
			return line[:index]
		}
	}

	return line
}

// closesArray reports whether value, the start of an array of TOML, holds
// its closing bracket, out of a string.
func closesArray(value string) bool {
	var quote rune

	for _, char := range value {
		switch {
		case quote != 0 && char == quote:
			quote = 0
		case quote != 0:
		case char == '"' || char == '\'':
			quote = char
		case char == ']':
			return true
		}
	}

	return false
}

// tomlStrings returns the strings of value, a string of TOML or an array of
// them, basic or literal, without escapes.
func tomlStrings(value string) []string {
	var (
		values  []string
		quote   rune
		current strings.Builder
	)

	for _, char := range value {
		switch {
		case quote != 0 && char == quote:
			values = append(values, current.String())
			quote = 0

			current.Reset()
		case quote != 0:
			current.WriteRune(char)
		case char == '"' || char == '\'':
			quote = char
		}
	}

	return values
}
