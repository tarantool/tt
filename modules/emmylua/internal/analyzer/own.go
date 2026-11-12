package analyzer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/tarantool/go-luarocks/tree"
)

// ownRocks is what the manifest of a project names its own rocks by: the
// package, which tt package build installs as the rock of its name, and the
// products released as a rock of another name.
type ownRocks struct {
	Package struct {
		Name string `toml:"name"`
	} `toml:"package"`
	Products map[string]struct {
		Rock string `toml:"rock"`
	} `toml:"products"`
}

// OwnModules returns what the rocks of the project's own package hold in
// .rocks/share/tarantool, the library of the modules of its rocks: tt package
// build lays the package out there, its components under their namespaces,
// and installs it as a rock. Those are copies of the project's own code,
// which EmmyLua reads from its sources already; read again as a library,
// every class of the project is declared twice. The rocks are the one of the
// package name and those of the products released under another
// (app.manifest.toml), every version installed, and their files are those
// the rock_manifest of each lists among its Lua modules. A directory at the
// top of the library is given whole when no other rock has a module in it,
// the files alone otherwise. A project with no rocks installed, or none of
// its own, has none.
func OwnModules(ctx context.Context, projectDir string) ([]string, error) {
	names, err := ownRockNames(projectDir)
	if err != nil {
		return nil, err
	}

	if len(names) == 0 {
		return nil, nil
	}

	rocksDir := filepath.Join(projectDir, rocksDirName)

	view, err := tree.Inspect(ctx, rocksDir)
	if err != nil {
		return nil, fmt.Errorf("reading the rocks of the project: %w", err)
	}

	own, others, err := luaModules(view, names)
	if err != nil {
		return nil, err
	}

	return topEntries(tree.Paths{Tree: rocksDir}.DeployLuaDir(), own, others), nil
}

// ownRockNames returns the names of the project's own rocks, as its
// manifest gives them (ownRocks).
func ownRockNames(projectDir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(projectDir, manifestFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading the manifest: %w", err)
	}

	var manifest ownRocks

	err = toml.Unmarshal(data, &manifest)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", manifestFileName, err)
	}

	var names []string

	if manifest.Package.Name != "" {
		names = append(names, manifest.Package.Name)
	}

	for _, product := range manifest.Products {
		if product.Rock != "" && !slices.Contains(names, product.Rock) {
			names = append(names, product.Rock)
		}
	}

	return names, nil
}

// luaModules returns where the Lua modules of the rocks of view are: those of
// the rocks named names, and those of the others.
func luaModules(view *tree.View, names []string) ([]string, []string, error) {
	var own, others []string

	for _, ref := range view.Rocks() {
		files, err := view.Files(ref)
		if err != nil {
			return nil, nil, fmt.Errorf("reading the rocks of the project: %w", err)
		}

		for _, file := range files {
			if file.Section != tree.SectionLua {
				continue
			}

			if slices.Contains(names, ref.Name) {
				own = append(own, file.Path)
			} else {
				others = append(others, file.Path)
			}
		}
	}

	return own, others, nil
}

// topEntries returns own, files under dir, as the entries at the top of dir
// they are in: a file there, or a directory no file of others is in. The
// files of a directory others share are given alone. The result is sorted.
func topEntries(dir string, own, others []string) []string {
	shared := make(map[string]bool)

	for _, file := range others {
		if top, nested := topEntry(dir, file); nested {
			shared[top] = true
		}
	}

	var entries []string

	for _, file := range own {
		top, nested := topEntry(dir, file)

		switch {
		case top == "":
			continue
		case nested && !shared[top]:
			file = filepath.Join(dir, top)
		}

		if !slices.Contains(entries, file) {
			entries = append(entries, file)
		}
	}

	slices.Sort(entries)

	return entries
}

// topEntry returns the name of the entry at the top of dir that path is or
// is in, and whether path is below it; "" for a path outside dir.
func topEntry(dir, path string) (string, bool) {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}

	top, _, nested := strings.Cut(rel, string(filepath.Separator))

	return top, nested
}
