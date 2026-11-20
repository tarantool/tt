package cc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/tarantool/tt/sdk/build/v3"
)

// This file links what other components of the package made for linking:
// the libraries the output of their builds sends to = "link", which the core
// puts into their build.Dirs.Link, and which the build of a component that
// names them in link finds in build.Dirs.LinkOf.

// componentNameRe is the shape of the name of a component.
var componentNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// checkLink checks link, the components whose libraries the build of the
// component self links: each is named once, has the shape of the name of a
// component, and is not self. That each is a component the products build
// before self is the core's to check (build.Linker).
func checkLink(link []string, self string) error {
	for index, name := range link {
		field := fmt.Sprintf("link[%d]", index)

		switch {
		case !componentNameRe.MatchString(name):
			return build.FieldErrorf(field, "%q is not the name of a component", name)
		case name == self && self != "":
			return build.FieldErrorf(field, "%q is the component itself", name)
		case slices.Contains(link[:index], name):
			return build.FieldErrorf(field, "%q is listed twice", name)
		}
	}

	return nil
}

// linkedLibraries are the libraries the components of link made for
// linking.
type linkedLibraries struct {
	// dirs are the directories of the libraries, -L, one a component.
	dirs []string
	// includeDirs are the directories of their headers, -I: include/ of
	// those of dirs that have one.
	includeDirs []string
	// names are the libraries, -l<name>, in the order of the components and
	// of their names, each once: lib<name>.a, .so and .dylib of dirs.
	names []string
	// files are the files of dirs, of the link key, but the headers.
	files []string
}

// libraryNameRe matches the file of a library the linker takes by -l<name>:
// lib<name>.a, lib<name>.so and lib<name>.dylib.
var libraryNameRe = regexp.MustCompile(`^lib(.+)\.(a|so|dylib)$`)

// linkedLibrariesOf returns the libraries the components of link made for
// linking in the build of c, from the directory of each (build.Dirs.LinkOf).
// A component whose directory is not there was not built, and one whose
// directory holds no library made none: either is an error naming it.
func linkedLibrariesOf(link []string, c *build.Context) (linkedLibraries, error) {
	linked := linkedLibraries{dirs: nil, includeDirs: nil, names: nil, files: nil}

	for index, component := range link {
		field := fmt.Sprintf("link[%d]", index)
		dir := c.Dirs.LinkOf(component)

		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return linkedLibraries{}, build.FieldErrorf(field, "component %q has not been "+
				"built: build it first, as a build of the whole product does before %q",
				component, c.Component.Name)
		}

		if err != nil {
			return linkedLibraries{}, fmt.Errorf("the libraries of component %q: %w",
				component, err)
		}

		var names []string

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			linked.files = append(linked.files, filepath.Join(dir, entry.Name()))

			match := libraryNameRe.FindStringSubmatch(entry.Name())
			if match != nil && !slices.Contains(names, match[1]) {
				names = append(names, match[1])
			}
		}

		if len(names) == 0 {
			return linkedLibraries{}, build.FieldErrorf(field, "component %q made no "+
				"library to link, lib<name>.a, .so or .dylib: its output sends one to = "+
				"\"link\"", component)
		}

		linked.dirs = append(linked.dirs, dir)

		include := filepath.Join(dir, "include")

		info, err := os.Stat(include)
		if err == nil && info.IsDir() {
			linked.includeDirs = append(linked.includeDirs, include)
		}

		for _, name := range names {
			if !slices.Contains(linked.names, name) {
				linked.names = append(linked.names, name)
			}
		}
	}

	return linked, nil
}

// includeFlags are the -I of the directories of the headers of linked.
func (l linkedLibraries) includeFlags() []string {
	flags := make([]string, 0, len(l.includeDirs))

	for _, dir := range l.includeDirs {
		flags = append(flags, "-I"+dir)
	}

	return flags
}
