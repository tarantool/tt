package coredump

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

	"github.com/tarantool/tt/sdk/execfile"
)

// dirMode is the mode of the directories the extraction creates.
const dirMode = 0o755

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
// created if it does not exist, with the modes of its entries and its
// symbolic links. An entry whose name is absolute or leaves the directory
// once cleaned is an error, and so is a symbolic link whose target is
// absolute or, resolved from the directory of the link, leaves the
// directory. The files are written through an [os.Root] of the directory,
// so no write follows a symbolic link out of it. Directory entries are
// skipped: the directory of every file is created, with 0755, before the
// file. A file that exists is overwritten.
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

	output, err := e.root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		header.FileInfo().Mode().Perm())
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
