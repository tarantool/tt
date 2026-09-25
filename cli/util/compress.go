package util

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// makeTarGzReader makes reader for tar.gz file.
func makeTarGzReader(archive *os.File) (*tar.Reader, error) {
	uncompressedStream, err := gzip.NewReader(archive)
	if err != nil {
		return nil, fmt.Errorf("cannot decompress %q: %w", archive.Name(), err)
	}

	tarReader := tar.NewReader(uncompressedStream)

	return tarReader, nil
}

// ExtractTarGz extracts tar.gz archive.
func ExtractTarGz(tarName, dstDir string) error {
	archive, err := os.Open(tarName)
	if err != nil {
		return fmt.Errorf("cannot open the archive: %w", err)
	}

	defer func() {
		_ = archive.Close()
	}()

	tarReader, err := makeTarGzReader(archive)
	if err != nil {
		return err
	}

	for {
		header, err := tarReader.Next()

		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("cannot read %q: %w", tarName, err)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
			// Some archives have strange order of objects,
			// so we check that all folders exist before
			// creating a file.
			dirName := filepath.Dir(header.Name)

			_, err := os.Stat(filepath.Join(dstDir, dirName))
			if os.IsNotExist(err) {
				// 0755:
				//    user:   read/write/execute
				//    group:  read/execute
				//    others: read/execute
				_ = os.MkdirAll(filepath.Join(dstDir, dirName), archiveDirectoryMode)
			}

			//nolint:gosec // Extraction trusts the archive: entry names are joined as is.
			outFile, err := os.OpenFile(filepath.Join(dstDir, header.Name),
				os.O_CREATE|os.O_WRONLY, header.FileInfo().Mode().Perm())
			if err != nil {
				return fmt.Errorf("cannot create an extracted file: %w", err)
			}

			_, err = io.Copy(outFile, tarReader)
			if err != nil {
				_ = outFile.Close()
				return fmt.Errorf("cannot extract %q: %w", header.Name, err)
			}

			_ = outFile.Close()
		case tar.TypeSymlink:
			//nolint:gosec // Extraction trusts the archive: entry names are joined as is.
			err := os.Symlink(header.Linkname, filepath.Join(dstDir, header.Name))
			if err != nil {
				return fmt.Errorf("cannot create an extracted symlink: %w", err)
			}
		default:
			return fmt.Errorf("%w%b in %s",
				errUnknownArchiveEntryType, header.Typeflag, header.Name)
		}
	}

	return nil
}
