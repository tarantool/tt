package coredump

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"syscall"

	"github.com/tarantool/tt/sdk/execfile"
)

// dirMode is the mode of the directories the extraction and the pack
// create: their user's alone, as the files of an archive are.
const dirMode = 0o700

// extractedPerm is what of the permissions of an entry the file extracted
// from it keeps: the bits of its owner alone. An archive of pack holds the
// memory of a process, its core and the tails of the logs of its instance,
// which are for the owner of the process alone, and the user who unpacks
// it owns the files; the entries carry the modes of the files tar packed,
// 0644 of a core gcore wrote or of an archive of a tt before, which
// unpacked as they were for every user to read.
const extractedPerm fs.FileMode = 0o700

const (
	// sparseBlock is the size of the blocks writeSparse writes a file in: a
	// block of zeros is skipped rather than written, and stays a hole of the
	// file. It is the block of the file systems that keep holes, ext4, XFS
	// and Btrfs, and the page of tmpfs on amd64: a hole smaller than their
	// block takes the block on the disk anyway.
	sparseBlock = 4 << 10
	// sparseBuffer is how much writeSparse reads at a time, a whole number
	// of sparseBlocks, so that every block but the last of a file begins at
	// an offset of the file that is a multiple of sparseBlock.
	sparseBuffer = 256 * sparseBlock
)

var (
	// errUnsafePath reports an entry whose name is outside the destination.
	errUnsafePath = errors.New("archive entry is outside the destination")
	// errUnsafeLink reports a symbolic link that points outside the
	// destination.
	errUnsafeLink = errors.New("archive symlink points outside the destination")
	// errUnknownType reports an entry of a type the extraction does not take.
	errUnknownType = errors.New("unknown type: ")
)

// copyEmbedded writes the file src of fsys to dst with perms, as a file a
// process may run: the scripts are run right after they are written.
func copyEmbedded(fsys fs.FS, src, dst string, perms fs.FileMode) error {
	data, err := fs.ReadFile(fsys, src)
	if err != nil {
		return fmt.Errorf("cannot read the source file: %w", err)
	}

	err = execfile.WriteFile(dst, data, perms)
	if err != nil {
		return fmt.Errorf("cannot write the destination file: %w", err)
	}

	return nil
}

// extractTarGz extracts the tar.gz archive at archivePath into dir, which is
// created if it does not exist, with the modes of its entries, of their
// owner alone (see extractedPerm), and its symbolic links. An entry whose
// name is absolute or leaves the directory once cleaned is an error, and so
// is a symbolic link whose target is absolute or, resolved from the
// directory of the link, leaves the directory. The files are written
// through an [os.Root] of the directory, so no write follows a symbolic
// link out of it. Directory entries are skipped: the directory of every
// file is created, with 0700, before the file. A file that exists is
// overwritten, and gets the mode of the entry. The blocks of zeros of a
// file, the holes of a core, are left as holes (see writeSparse).
func extractTarGz(archivePath, dir string) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("cannot open the archive: %w", err)
	}

	defer func() {
		_ = archive.Close()
	}()

	stream, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("cannot decompress %q: %w", archivePath, err)
	}

	err = os.MkdirAll(dir, dirMode)
	if err != nil {
		return fmt.Errorf("cannot create the destination: %w", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("cannot open the destination: %w", err)
	}

	defer func() {
		_ = root.Close()
	}()

	ext := extractor{root: root, links: nil}

	reader := tar.NewReader(stream)

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("cannot read %q: %w", archivePath, err)
		}

		err = ext.entry(header, reader)
		if err != nil {
			return err
		}
	}

	return ext.checkLinks()
}

// unpackedDir returns the directory of the artefacts of an archive
// extracted into dir, relative to it: the one entry of dir when it is a
// directory, as pack puts the artefacts into tarantool-core-<pid>-<time>-
// <host>/ and names the archive after it, else dir itself, ".", of an
// archive of the artefacts at its root. The name of the archive does not
// tell it: an archive renamed keeps the directory it holds.
func unpackedDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("cannot read the unpacked archive: %w", err)
	}

	if len(entries) == 1 && entries[0].IsDir() {
		return entries[0].Name(), nil
	}

	return ".", nil
}

// extractor writes the entries of one archive into root.
type extractor struct {
	root *os.Root
	// links are the names of the symbolic links extracted.
	links []string
}

// entry extracts one entry, the content of a file read from content.
func (e *extractor) entry(header *tar.Header, content io.Reader) error {
	name := path.Clean(header.Name)
	if !filepath.IsLocal(name) {
		return fmt.Errorf("%w: %q", errUnsafePath, header.Name)
	}

	switch header.Typeflag {
	case tar.TypeDir:
		return nil
	case tar.TypeReg:
		return e.file(header, name, content)
	case tar.TypeSymlink:
		return e.symlink(header, name)
	default:
		return fmt.Errorf("%w%b in %s", errUnknownType, header.Typeflag, header.Name)
	}
}

// file extracts a regular file, creating its directory first: an archive
// may list a file before its directory, or not list the directory at all.
func (e *extractor) file(header *tar.Header, name string, content io.Reader) error {
	err := e.root.MkdirAll(path.Dir(name), dirMode)
	if err != nil {
		return fmt.Errorf("cannot create an extracted directory: %w", err)
	}

	perm := header.FileInfo().Mode().Perm() & extractedPerm

	output, err := e.root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("cannot create an extracted file: %w", err)
	}

	// The mode of OpenFile is that of a file it creates: a file there
	// already kept its own.
	err = output.Chmod(perm)
	if err == nil {
		err = writeSparse(output, content)
	}

	if err != nil {
		_ = output.Close()

		return fmt.Errorf("cannot extract %q: %w", header.Name, err)
	}

	err = output.Close()
	if err != nil {
		return fmt.Errorf("cannot extract %q: %w", header.Name, err)
	}

	return nil
}

// writeSparse writes what content reads to output, an empty file, leaving
// the blocks of zeros holes: each sparseBlock of zeros is skipped rather
// than written, and the file is truncated to the size read at the end, so
// that a hole at its end stays too. Any file may be written so: one without
// a block of zeros is written as it is, and a hole reads as zeros.
//
// A core is mostly holes, the pages of the mappings the process never
// touched. Linux writes them as zeros to a pipe, the core of
// kernel.core_pattern pack stores (see storeCore). pack archives a core with
// tar --sparse, which keeps its data and a map of its holes, and archive/tar
// reads the holes back as zeros: it expands the map itself and tells no
// hole, and written out as read, a core of 11 MB on the disk and 1.1 GB in
// size took 1.1 GB when it was extracted (see extractTarGz).
func writeSparse(output *os.File, content io.Reader) error {
	buffer := make([]byte, sparseBuffer)
	zeros := make([]byte, sparseBlock)

	// isHole reports whether the block of the buffer at start is zeros.
	isHole := func(start, read int) bool {
		end := min(start+sparseBlock, read)

		return bytes.Equal(buffer[start:end], zeros[:end-start])
	}

	var size int64

	for {
		read, readErr := fill(content, buffer)

		// The blocks of the same kind in a row are written, or skipped, at
		// once.
		for start := 0; start < read; {
			hole := isHole(start, read)

			end := min(start+sparseBlock, read)
			for end < read && isHole(end, read) == hole {
				end = min(end+sparseBlock, read)
			}

			var err error

			if hole {
				_, err = output.Seek(int64(end-start), io.SeekCurrent)
			} else {
				_, err = output.Write(buffer[start:end])
			}

			if err != nil {
				return fmt.Errorf("cannot write the content: %w", err)
			}

			start = end
		}

		size += int64(read)

		if errors.Is(readErr, io.EOF) {
			break
		} else if readErr != nil {
			return fmt.Errorf("cannot read the content: %w", readErr)
		}
	}

	err := output.Truncate(size)
	if err != nil {
		return fmt.Errorf("cannot write the content: %w", err)
	}

	return nil
}

// fill reads content into buffer until the buffer is full or content ends,
// and returns how much it read and the error that stopped it, io.EOF at the
// end. io.ReadFull would not do: it reports a short read at the end as
// io.ErrUnexpectedEOF, the error archive/tar reports a truncated entry
// with, which would end the file short without an error.
func fill(content io.Reader, buffer []byte) (int, error) {
	read := 0

	for read < len(buffer) {
		count, err := content.Read(buffer[read:])

		read += count
		if err != nil {
			return read, err //nolint:wrapcheck // The caller wraps it.
		}
	}

	return read, nil
}

// symlink extracts a symbolic link whose target, read from the directory of
// the link, stays in the destination.
func (e *extractor) symlink(header *tar.Header, name string) error {
	target := header.Linkname
	//nolint:gosec // G305: the joined path is only checked, it names no file to write.
	if path.IsAbs(target) || !filepath.IsLocal(path.Join(path.Dir(name), target)) {
		return fmt.Errorf("%w: %q -> %q", errUnsafeLink, header.Name, target)
	}

	err := e.root.Symlink(target, name)
	if err != nil {
		return fmt.Errorf("cannot create an extracted symlink: %w", err)
	}

	e.links = append(e.links, name)

	return nil
}

// checkLinks resolves the extracted links through the destination, once
// every entry is there: a link that is inside the destination by its name
// can leave it through another link, one to "." in a directory of its path.
// A link that points to nothing or loops is no escape.
func (e *extractor) checkLinks() error {
	for _, name := range e.links {
		_, err := e.root.Stat(name)
		if err == nil || errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ELOOP) ||
			errors.Is(err, syscall.ENOTDIR) {
			continue
		}

		return fmt.Errorf("%w: %q: %w", errUnsafeLink, name, err)
	}

	return nil
}
