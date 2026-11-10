package wasm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/v3/internal/execfile"
)

const (
	// maxTreeSize bounds what one archive unpacks to.
	maxTreeSize = 4 << 30
	// ownerWrite lets tt replace a file it unpacked read-only.
	ownerWrite os.FileMode = 0o600
)

var (
	// errUnsafeEntry reports an archive entry that would land outside the
	// tree: an absolute path, a .. or a link out of it.
	errUnsafeEntry = errors.New("unsafe archive entry")
	// errTreeTooLarge reports an archive that unpacks to more than maxTreeSize.
	errTreeTooLarge = errors.New("the archive unpacks to too much")
)

// unpackTree unpacks the .tar.gz archive into dest, its top directory
// stripped, over whatever dest holds: an archive laid over another replaces
// the files they share. Directories, regular files, symbolic links that stay
// inside the tree and hard links are unpacked; an entry that would land
// outside dest fails the whole unpacking.
func unpackTree(archive []byte, dest string) error {
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("reading the archive: %w", err)
	}

	reader := tar.NewReader(compressed)

	var total int64

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("reading the archive: %w", err)
		}

		rel, ok := stripTop(header.Name)
		if !ok {
			return fmt.Errorf("%w: %s", errUnsafeEntry, header.Name)
		}

		if rel == "" {
			continue
		}

		target := filepath.Join(dest, filepath.FromSlash(rel))

		total += header.Size
		if total > maxTreeSize {
			return errTreeTooLarge
		}

		err = unpackEntry(reader, header, dest, rel, target)
		if err != nil {
			return err
		}
	}
}

// unpackEntry writes one entry of the archive at target.
func unpackEntry(reader io.Reader, header *tar.Header, dest, rel, target string) error {
	switch header.Typeflag {
	case tar.TypeDir:
		return mkdir(target)
	case tar.TypeReg:
		return writeEntry(reader, target, header.FileInfo().Mode().Perm())
	case tar.TypeSymlink:
		// A link is kept as it is, but only one that points inside the tree.
		resolved := path.Join(path.Dir(rel), header.Linkname) //nolint:gosec // Checked below.
		if path.IsAbs(header.Linkname) || !insideTree(resolved) {
			return fmt.Errorf("%w: %s -> %s", errUnsafeEntry, header.Name, header.Linkname)
		}

		return replaceWith(target, func() error { return os.Symlink(header.Linkname, target) })
	case tar.TypeLink:
		linked, ok := stripTop(header.Linkname)
		if !ok || linked == "" {
			return fmt.Errorf("%w: %s => %s", errUnsafeEntry, header.Name, header.Linkname)
		}

		source := filepath.Join(dest, filepath.FromSlash(linked))

		return replaceWith(target, func() error { return os.Link(source, target) })
	default:
		// Devices, fifos and the like have no place in a toolchain.
		return nil
	}
}

// stripTop drops the top directory of an archive path and reports whether
// the rest stays inside the tree.
func stripTop(name string) (string, bool) {
	name = strings.TrimPrefix(name, "./")
	if path.IsAbs(name) {
		return "", false
	}

	_, rest, _ := strings.Cut(name, "/")
	if rest == "" {
		return "", true
	}

	rest = path.Clean(rest)

	return rest, insideTree(rest)
}

// insideTree reports whether a cleaned relative path stays inside its root.
func insideTree(rel string) bool {
	rel = path.Clean(rel)

	return rel != ".." && !strings.HasPrefix(rel, "../") && !path.IsAbs(rel)
}

// mkdir creates the directory dir and its parents.
func mkdir(dir string) error {
	err := os.MkdirAll(dir, dirPerm)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	return nil
}

// writeEntry writes the content of reader to target with mode.
func writeEntry(reader io.Reader, target string, mode os.FileMode) error {
	return replaceWith(target, func() error {
		err := execfile.Write(target, reader, mode|ownerWrite)
		if err != nil {
			return fmt.Errorf("creating %s: %w", target, err)
		}

		return nil
	})
}

// replaceWith creates target with create, over what is there.
func replaceWith(target string, create func() error) error {
	err := mkdir(filepath.Dir(target))
	if err != nil {
		return err
	}

	err = os.Remove(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("replacing %s: %w", target, err)
	}

	err = create()
	if err != nil {
		return fmt.Errorf("unpacking %s: %w", target, err)
	}

	return nil
}
