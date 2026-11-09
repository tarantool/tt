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
	// headersDir is where the archive of tarantool has the headers.
	headersDir = "include/tarantool"
)

// writeArchive writes the archive of tarantool sdk/bundle defines to dst:
// executable as tarantool at the root and the tree of headers as
// include/tarantool. A link among the headers is written as the file it
// leads to inside the tree; one that leads elsewhere, or to a directory, is
// an error.
func writeArchive(dst io.Writer, executable, headers string) error {
	compressed, err := gzip.NewWriterLevel(dst, gzip.BestSpeed)
	if err != nil {
		return fmt.Errorf("compress Tarantool archive: %w", err)
	}

	archive := tar.NewWriter(compressed)

	err = writeArchiveEntries(archive, executable, headers)
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

func writeArchiveEntries(archive *tar.Writer, executable, headers string) error {
	err := writeArchiveFile(archive, "tarantool", executable, executableMode)
	if err != nil {
		return err
	}

	for _, name := range []string{filepath.Dir(headersDir), headersDir} {
		err = writeArchiveDir(archive, name)
		if err != nil {
			return err
		}
	}

	root, err := os.OpenRoot(headers)
	if err != nil {
		return fmt.Errorf("open Tarantool headers: %w", err)
	}

	defer func() { _ = root.Close() }()

	//nolint:wrapcheck // The function below wraps every error it returns.
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("read Tarantool headers: %w", err)
		}

		if name == "." {
			return nil
		}

		// The root follows the links that stay inside the headers only.
		info, err := root.Stat(name)
		if err != nil {
			return fmt.Errorf("%w: header %s: %w", errPackageContents, name, err)
		}

		archiveName := headersDir + "/" + name

		switch {
		case entry.IsDir():
			return writeArchiveDir(archive, archiveName)
		case info.Mode().IsRegular():
			return writeArchiveRootFile(archive, archiveName, root, name, info.Mode().Perm())
		default:
			return fmt.Errorf("%w: header %s is not a regular file", errPackageContents, name)
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
