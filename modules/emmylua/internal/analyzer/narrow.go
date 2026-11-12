package analyzer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ErrOutside reports a path given to check or doc that is not in the
// workspace.
var ErrOutside = errors.New("not in the workspace")

// Narrowing is the configuration that narrows a run of emmylua_check or
// emmylua_doc_cli on the root of the workspace to some paths of it
// (Narrow), and the directory it lives in for the run.
type Narrowing struct {
	// Config is the configuration to give the program after the one tt
	// writes; empty when the paths narrow nothing.
	Config string
	// dir holds Config and the links of the library.
	dir string
}

// Remove removes the files of the narrowing.
func (n *Narrowing) Remove() error {
	if n.dir == "" {
		return nil
	}

	err := os.RemoveAll(n.dir)
	if err != nil {
		return fmt.Errorf("removing %s: %w", n.dir, err)
	}

	return nil
}

// narrowingLibrary is the directory of the links Narrow makes, in the
// directory of the narrowing.
const narrowingLibrary = "lib"

// Narrow writes, into a new directory under parent, a configuration that
// narrows a run of the programs on root, the workspace, to paths, absolute
// files and directories inside it: the programs check and document only
// what lies under paths, while the workspace, and so the names of the
// modules, the paths of the report and the relative paths of the
// configuration, stays root. EmmyLua has no such setting; it is made of two:
//
//   - every entry of root that neither is a path nor leads to one goes to
//     workspace.ignoreDir, so it is not read as the code of the workspace;
//   - the same entries, but .rocks/, .tarantool/ and _build/ at the top,
//     which are not the code of the project, are read as a library, so that
//     their modules resolve and are not reported: a directory of links to
//     them, laid out as they are under root, so that their modules keep the
//     names they have in root. The links are not under the paths EmmyLua
//     ignores, and it follows them.
//
// A path that is root itself narrows nothing: the narrowing has no Config. A
// path outside root, once links are resolved, fails with an error wrapping
// ErrOutside. The caller removes the narrowing after the run.
func Narrow(parent, root string, paths []string) (*Narrowing, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("reading the workspace: %w", err)
	}

	keep := make([]string, 0, len(paths))

	for _, path := range paths {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}

		if resolved == realRoot {
			return &Narrowing{Config: "", dir: ""}, nil
		}

		if !strings.HasPrefix(resolved, realRoot+string(filepath.Separator)) {
			return nil, fmt.Errorf("%w: %s is not in %s", ErrOutside, path, root)
		}

		keep = append(keep, resolved)
	}

	others, err := complement(realRoot, keep)
	if err != nil {
		return nil, err
	}

	err = os.MkdirAll(parent, dirPerm)
	if err != nil {
		return nil, fmt.Errorf("writing the EmmyLua configuration: %w", err)
	}

	dir, err := os.MkdirTemp(parent, "narrow-")
	if err != nil {
		return nil, fmt.Errorf("writing the EmmyLua configuration: %w", err)
	}

	narrowing := &Narrowing{Config: filepath.Join(dir, ConfigFileName), dir: dir}

	err = narrowing.write(realRoot, others)
	if err != nil {
		_ = narrowing.Remove()

		return nil, err
	}

	return narrowing, nil
}

// write writes the configuration and the links of the narrowing that leaves
// others, entries of root, out of the workspace.
func (n *Narrowing) write(root string, others []string) error {
	library := filepath.Join(n.dir, narrowingLibrary)

	for _, entry := range others {
		rel, err := filepath.Rel(root, entry)
		if err != nil {
			return fmt.Errorf("narrowing the workspace: %w", err)
		}

		if slices.Contains(notCode, rel) {
			continue
		}

		link := filepath.Join(library, rel)

		err = os.MkdirAll(filepath.Dir(link), dirPerm)
		if err == nil {
			err = os.Symlink(entry, link)
		}

		if err != nil {
			return fmt.Errorf("narrowing the workspace: %w", err)
		}
	}

	workspace := map[string]any{"ignoreDir": others}

	_, err := os.Stat(library)
	if err == nil {
		workspace["library"] = []string{library}
	}

	data, err := json.MarshalIndent(map[string]any{
		"$comment": "Written by tt emmylua for one run on some paths of " + root +
			", and removed after it.",
		"workspace": workspace,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("rendering the EmmyLua configuration: %w", err)
	}

	return writeFileAtomic(n.Config, append(data, '\n'))
}

// notCode are the entries at the top of a workspace that are not its code:
// the configuration tt writes leaves them out of it (Layout.Config).
//
//nolint:gochecknoglobals // A constant list: Go has no constant slices.
var notCode = []string{rocksDirName, projectDirName, buildDirName}

// complement returns the entries of root that are none of keep, paths under
// root, and lead to none of them, in the order of the walk: the entries of
// root, and of every directory on the way to a path of keep, but those.
//
// It is never nil: none is an empty list, which the configuration writes as
// [], since EmmyLua refuses a null ignoreDir and drops the whole file.
func complement(root string, keep []string) ([]string, error) {
	others := []string{}

	var visit func(dir string) error

	visit = func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("narrowing the workspace: %w", err)
		}

		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())

			switch {
			case slices.Contains(keep, path):
			case slices.ContainsFunc(keep, func(kept string) bool {
				return strings.HasPrefix(kept, path+string(filepath.Separator))
			}):
				err = visit(path)
				if err != nil {
					return err
				}
			default:
				others = append(others, path)
			}
		}

		return nil
	}

	err := visit(root)
	if err != nil {
		return nil, err
	}

	return others, nil
}
