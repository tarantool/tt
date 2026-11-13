// Package untar extracts tar.gz archives into a directory and refuses the
// entries of an archive that would reach outside of it.
//
// An entry whose name is absolute or leaves the directory once cleaned is an
// error, and so is a symbolic link whose target is absolute or, resolved from
// the directory of the link, leaves the directory. The files are written
// through an [os.Root] of the directory, so no write follows a symbolic link
// out of it, one of the archive's or one that was there before.
package untar

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"syscall"
)

const (
	// dirMode is the mode of the directories the extraction creates.
	dirMode = 0o755
	// fileMode is the mode of a file when the entry modes are not kept.
	fileMode = 0o666
)

var (
	// ErrUnsafePath reports an entry whose name is outside the destination.
	ErrUnsafePath = errors.New("archive entry is outside the destination")
	// ErrUnsafeLink reports a symbolic link that points outside the destination.
	ErrUnsafeLink = errors.New("archive symlink points outside the destination")
	// ErrUnknownType reports an entry of a type the extraction does not take.
	ErrUnknownType = errors.New("unknown type: ")
)

// Options are what an archive may hold beside directories and regular files.
type Options struct {
	// Symlinks lets the archive hold symbolic links; otherwise one is an
	// entry of an unknown type.
	Symlinks bool
	// EntryModes creates a regular file with the permissions of its entry;
	// otherwise with 0666. The umask applies either way.
	EntryModes bool
}

// ExtractTarGz extracts the tar.gz archive at archivePath into dir, which is
// created if it does not exist. Directory entries are skipped: the directory
// of every file is created, with 0755, before the file. A file that exists is
// overwritten.
func ExtractTarGz(archivePath, dir string, opts Options) error {
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

	ext := extractor{root: root, opts: opts, links: nil}

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

// extractor writes the entries of one archive into root.
type extractor struct {
	root *os.Root
	opts Options
	// links are the names of the symbolic links extracted.
	links []string
}

// entry extracts one entry, the content of a file read from content.
func (e *extractor) entry(header *tar.Header, content io.Reader) error {
	name := path.Clean(header.Name)
	if !filepath.IsLocal(name) {
		return fmt.Errorf("%w: %q", ErrUnsafePath, header.Name)
	}

	switch {
	case header.Typeflag == tar.TypeDir:
		return nil
	case header.Typeflag == tar.TypeReg:
		return e.file(header, name, content)
	case header.Typeflag == tar.TypeSymlink && e.opts.Symlinks:
		return e.symlink(header, name)
	default:
		return fmt.Errorf("%w%b in %s", ErrUnknownType, header.Typeflag, header.Name)
	}
}

// file extracts a regular file, creating its directory first: some archives
// list a file before its directory, or not the directory at all.
func (e *extractor) file(header *tar.Header, name string, content io.Reader) error {
	err := e.root.MkdirAll(path.Dir(name), dirMode)
	if err != nil {
		return fmt.Errorf("cannot create an extracted directory: %w", err)
	}

	mode := os.FileMode(fileMode)
	if e.opts.EntryModes {
		mode = header.FileInfo().Mode().Perm()
	}

	output, err := e.root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("cannot create an extracted file: %w", err)
	}

	_, err = io.Copy(output, content)
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

// symlink extracts a symbolic link whose target, read from the directory of
// the link, stays in the destination.
func (e *extractor) symlink(header *tar.Header, name string) error {
	target := header.Linkname
	//nolint:gosec // G305: the joined path is only checked, it names no file to write.
	if path.IsAbs(target) || !filepath.IsLocal(path.Join(path.Dir(name), target)) {
		return fmt.Errorf("%w: %q -> %q", ErrUnsafeLink, header.Name, target)
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

		return fmt.Errorf("%w: %q: %w", ErrUnsafeLink, name, err)
	}

	return nil
}
