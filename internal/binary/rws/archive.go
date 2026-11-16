package rws

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	dirMode        = 0o755
	executableMode = 0o755
	fileMode       = 0o644
	// headersDir is where the archive of tarantool has the headers.
	headersDir = "include/tarantool"
	// shareDir is where the archive of tarantool has the Lua files of
	// Tarantool, the EmmyLua stubs of its built-in modules among them.
	shareDir = "share/tarantool"
)

// archiveContents are the files of a build of Tarantool the archive of
// tarantool holds.
type archiveContents struct {
	// executable is the tarantool executable.
	executable string
	// headers is the tree of the headers, include/tarantool of the archive.
	headers string
	// share is the tree of share/tarantool of the archive, or empty for a
	// build without one.
	share string
	// licenses are the license files, LICENSE and AUTHORS, at the root of
	// the archive.
	licenses []string
}

// writeArchive writes the archive of tarantool sdk/bundle defines to dst:
// the executable as tarantool at the root, the tree of headers as
// include/tarantool, that of share as share/tarantool and the license files
// at the root. A link among the headers is written as the file it leads to
// inside the tree; one that leads elsewhere, or to a directory, is an
// error. One under share is skipped instead.
func writeArchive(dst io.Writer, contents archiveContents) error {
	compressed, err := gzip.NewWriterLevel(dst, gzip.BestSpeed)
	if err != nil {
		return fmt.Errorf("compress Tarantool archive: %w", err)
	}

	archive := tar.NewWriter(compressed)

	err = writeArchiveEntries(archive, contents)
	if err != nil {
		return err
	}

	err = archive.Close()
	if err != nil {
		return fmt.Errorf("write Tarantool archive: %w", err)
	}

	err = compressed.Close()
	if err != nil {
		return fmt.Errorf("compress Tarantool archive: %w", err)
	}

	return nil
}

func writeArchiveEntries(archive *tar.Writer, contents archiveContents) error {
	err := writeArchiveFile(archive, "tarantool", contents.executable, executableMode)
	if err != nil {
		return err
	}

	for _, license := range contents.licenses {
		err = writeArchiveFile(archive, filepath.Base(license), license, fileMode)
		if err != nil {
			return err
		}
	}

	err = writeArchiveTree(archive, contents.headers, headersDir, true)
	if err != nil {
		return err
	}

	if contents.share == "" {
		return nil
	}

	return writeArchiveTree(archive, contents.share, shareDir, false)
}

// writeArchiveTree writes the tree at directory as the directory name of
// the archive, its parents included. strict makes an entry other than a
// directory or a regular file, a link that leads out of the tree say, an
// error; otherwise it is skipped.
func writeArchiveTree(archive *tar.Writer, directory, name string, strict bool) error {
	for _, parent := range []string{filepath.Dir(name), name} {
		err := writeArchiveDir(archive, parent)
		if err != nil {
			return err
		}
	}

	root, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("open Tarantool %s: %w", name, err)
	}

	defer func() { _ = root.Close() }()

	//nolint:wrapcheck // The function below wraps every error it returns.
	return fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("read Tarantool %s: %w", name, err)
		}

		if path == "." {
			return nil
		}

		archiveName := name + "/" + path

		// The root follows the links that stay inside the tree only.
		info, err := root.Stat(path)

		switch {
		case err == nil && entry.IsDir():
			return writeArchiveDir(archive, archiveName)
		case err == nil && info.Mode().IsRegular():
			return writeArchiveRootFile(archive, archiveName, root, path, info.Mode().Perm())
		case !strict:
			// A link that leads out of the tree, say, is left out.
			return nil
		case err != nil:
			return fmt.Errorf("%w: %s: %w", errPackageContents, archiveName, err)
		default:
			return fmt.Errorf("%w: %s is not a regular file", errPackageContents, archiveName)
		}
	})
}

func writeArchiveDir(archive *tar.Writer, name string) error {
	err := archive.WriteHeader(&tar.Header{
		Typeflag: tar.TypeDir, Name: name + "/", Mode: dirMode,
	})
	if err != nil {
		return fmt.Errorf("write Tarantool archive: %w", err)
	}

	return nil
}

func writeArchiveFile(archive *tar.Writer, name, filename string, mode fs.FileMode) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open %s: %w", name, err)
	}

	defer func() { _ = file.Close() }()

	return writeArchiveContent(archive, name, file, mode)
}

func writeArchiveRootFile(archive *tar.Writer, archiveName string, root *os.Root, name string,
	mode fs.FileMode,
) error {
	file, err := root.Open(name)
	if err != nil {
		return fmt.Errorf("open header %s: %w", name, err)
	}

	defer func() { _ = file.Close() }()

	return writeArchiveContent(archive, archiveName, file, mode)
}

var errFileChanged = errors.New("file changed while archived")

func writeArchiveContent(archive *tar.Writer, name string, file *os.File, mode fs.FileMode,
) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", name, err)
	}

	err = archive.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: name, Mode: int64(mode), Size: info.Size(),
	})
	if err != nil {
		return fmt.Errorf("write Tarantool archive: %w", err)
	}

	written, err := io.Copy(archive, file)
	if err != nil {
		return fmt.Errorf("write %s to Tarantool archive: %w", name, err)
	}

	if written != info.Size() {
		return fmt.Errorf("%w: %s", errFileChanged, name)
	}

	return nil
}
