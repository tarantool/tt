package rocktree

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/tarantool/tt/v3/internal/execfile"
)

// StageDoc is the directory of a binary rock that holds its documents, the
// license files of a package among them. LuaRocks unpacks it, as every
// directory of a rock but lua/, lib/ and bin/, into the install dir of the
// rock, share/tarantool/rocks/<name>/<version>/doc/, and lists its files in
// the rock_manifest. No stage has one, so it changes nothing the rock
// deploys.
const StageDoc = "doc"

// WithDocs lays out the files of a rock in a directory of its own under temp
// and returns it, with the cleanup that removes it: the stage stage, copied as
// it is (MirrorTree), and in its doc/ what fill puts into the directory it is
// given, which does not exist yet. An error of fill is returned as it is.
//
// The documents are not the build's, so they do not go into the stage: a
// build removes from it what no component owns, and a later build or install
// of the stage would carry what it does not remove.
func WithDocs(temp, stage string, fill func(doc string) error) (string, func(), error) {
	dir, err := os.MkdirTemp(temp, "rock-")
	if err != nil {
		return "", nil, fmt.Errorf("creating the tree of the rock: %w", err)
	}

	cleanup := func() { _ = os.RemoveAll(dir) }

	err = MirrorTree(stage, dir)
	if err != nil {
		cleanup()

		return "", nil, fmt.Errorf("copying the stage of the rock: %w", err)
	}

	err = fill(filepath.Join(dir, StageDoc))
	if err != nil {
		cleanup()

		return "", nil, err
	}

	return dir, cleanup, nil
}

// MirrorTree copies src, a file or a directory, to dst as a rock is installed
// from it (tree.Tx.InstallStaged): a file with the permission bits it has,
// the umask not applied, which a rock records for a program, and a symlink as
// the link it is, which an install refuses when it leads out of the tree.
// The parents of dst are created. A missing src copies nothing, and what is
// neither a file, a directory nor a symlink is left out.
func MirrorTree(src, dst string) error {
	_, err := os.Lstat(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	err = os.MkdirAll(filepath.Dir(dst), dirPerm)
	if err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}

	//nolint:wrapcheck // Every error the walk yields already names the entry it failed on.
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("copying %s: %w", path, err)
		}

		target := filepath.Join(dst, rel)

		switch {
		case entry.IsDir():
			return os.MkdirAll(target, dirPerm)
		case entry.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}

			//nolint:gosec // dst is a fresh directory of tt's, src the stage of a build.
			return os.Symlink(link, target)
		case entry.Type().IsRegular():
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("copying %s: %w", path, err)
			}

			return copyFileAs(path, target, info.Mode().Perm())
		default:
			return nil
		}
	})
}

// EntryFiles lists the files that entry, a path of the package in dir to a
// file or a directory - an entry of its license_files - names: the slash
// paths, relative to dir, of the regular files it is or holds, sorted. A
// symlink is followed, to a file or a directory, as tt package pack follows it
// when it copies the entry into the binary rock; below the entry, one that
// leads nowhere or back to a directory being walked, and what is neither a
// file nor a directory, are left out. A missing entry fails it with an error
// wrapping fs.ErrNotExist.
func EntryFiles(dir, entry string) ([]string, error) {
	var files []string

	err := entryFiles(filepath.Join(dir, entry), filepath.ToSlash(entry), true,
		map[string]bool{}, &files)
	if err != nil {
		return nil, err
	}

	slices.Sort(files)

	return files, nil
}

// entryFiles adds to files the slash path rel of path when it is a regular
// file, and those of the files below it when it is a directory not among
// walking, the real paths of the directories it is in. top marks the entry
// itself, which has to be there.
func entryFiles(path, rel string, top bool, walking map[string]bool, files *[]string) error {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) && !top {
		return nil
	}

	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	switch {
	case info.Mode().IsRegular():
		*files = append(*files, rel)

		return nil
	case !info.IsDir():
		return nil
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	if walking[resolved] {
		return nil
	}

	walking[resolved] = true
	defer delete(walking, resolved)

	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	for _, entry := range entries {
		err = entryFiles(filepath.Join(path, entry.Name()), rel+"/"+entry.Name(), false,
			walking, files)
		if err != nil {
			return err
		}
	}

	return nil
}

// CopyEntry copies the files of entry of the package in dir (EntryFiles) into
// doc, each at its path in the package, as tt package pack copies them into
// the binary rock: what a symlink leads to, executable (0755) when its owner
// may run it and 0644 otherwise, less the umask. A missing entry fails it
// with an error wrapping fs.ErrNotExist, before anything is copied.
func CopyEntry(dir, entry, doc string) error {
	files, err := EntryFiles(dir, entry)
	if err != nil {
		return err
	}

	for _, rel := range files {
		src := filepath.Join(dir, filepath.FromSlash(rel))

		info, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("copying %s: %w", src, err)
		}

		perm := filePerm
		if info.Mode().Perm()&0o100 != 0 {
			perm = execPerm
		}

		dst := filepath.Join(doc, filepath.FromSlash(rel))

		err = os.MkdirAll(filepath.Dir(dst), dirPerm)
		if err != nil {
			return fmt.Errorf("copying %s: %w", src, err)
		}

		err = writeFile(src, dst, perm)
		if err != nil {
			return err
		}
	}

	return nil
}

// copyFileAs copies the file src to dst, whose parent exists, with the
// permission bits perm, the umask not applied to them.
func copyFileAs(src, dst string, perm os.FileMode) error {
	err := writeFile(src, dst, perm)
	if err != nil {
		return err
	}

	err = os.Chmod(dst, perm)
	if err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}

	return nil
}

// writeFile writes what the file src holds to dst, whose parent exists,
// created with perm less the umask.
func writeFile(src, dst string, perm os.FileMode) error {
	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}

	defer func() { _ = source.Close() }()

	err = execfile.Write(dst, source, perm)
	if err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}

	return nil
}
