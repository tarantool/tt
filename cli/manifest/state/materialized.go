package state

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// MaterializedFile is the file, in the metadata directory of a tree
// (Layout.Manifests), that names the rocks the lock of the project pinned
// when tt last materialized the tree from it: tt package build and tt package
// fetch write it. It is tt's own install state, as the metadata of the guests
// beside it is, and never travels in an archive. Its name starts with a dot,
// which no package name does, so it is never read as a guest.
//
// It is what tells a rock the lock dropped from a rock the lock never held.
// The lock of the project is rewritten by tt package remove, add and update
// before anything materializes it, so the next materialization cannot tell
// from the lock alone what the one before installed; and a rock the lock
// never held - the luatest tt test installs for a project that declares none,
// a rock installed by hand - is not the lock's to remove.
const MaterializedFile = ".materialized"

// materializedHeader opens the file, for whoever finds it in the tree.
const materializedHeader = "# The rocks the lock of the project pinned when tt last " +
	"materialized this tree.\n"

// ReadMaterialized returns the rocks MaterializedFile of the tree of lay
// names, and whether there is one: a tree tt has not materialized yet, or
// one an older tt did, has none, which is no error.
func ReadMaterialized(lay Layout) (map[string]bool, bool, error) {
	path := filepath.Join(lay.Manifests, MaterializedFile)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}

		return nil, false, fmt.Errorf("reading %s: %w", path, err)
	}

	names := map[string]bool{}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		names[line] = true
	}

	err = scanner.Err()
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", path, err)
	}

	return names, true, nil
}

// WriteMaterialized records names as the rocks the lock of the project pins,
// in MaterializedFile of the tree of lay, one per line in name order. The
// file is replaced whole, through a rename, so a reader finds the old list or
// the new one and never a part of either.
func WriteMaterialized(lay Layout, names []string) error {
	sorted := slices.Compact(slices.Sorted(slices.Values(names)))

	var body strings.Builder

	body.WriteString(materializedHeader)

	for _, name := range sorted {
		body.WriteString(name)
		body.WriteByte('\n')
	}

	err := os.MkdirAll(lay.Manifests, metaDirPerm)
	if err != nil {
		return fmt.Errorf("creating %s: %w", lay.Manifests, err)
	}

	path := filepath.Join(lay.Manifests, MaterializedFile)

	temp, err := os.CreateTemp(lay.Manifests, MaterializedFile+".*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	defer func() { _ = os.Remove(temp.Name()) }()

	_, err = temp.WriteString(body.String())
	if err == nil {
		err = temp.Chmod(metaFilePerm)
	}

	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(temp.Name(), path)
	}

	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}
