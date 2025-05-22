package util

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// makeTarGzReader makes reader for tar.gz file.
func makeTarGzReader(archive *os.File) (*tar.Reader, error) {
	uncompressedStream, err := gzip.NewReader(archive)
	if err != nil {
		return nil, err
	}

	tarReader := tar.NewReader(uncompressedStream)
	return tarReader, err
}

// ExtractTarGz extracts tar.gz archive.
func ExtractTarGz(tarName, dstDir string) error {
	archive, err := os.Open(tarName)
	if err != nil {
		return err
	}
	defer archive.Close()
	tarReader, err := makeTarGzReader(archive)
	if err != nil {
		return err
	}
	for {
		header, err := tarReader.Next()

		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
			// Some archives have strange order of objects,
			// so we check that all folders exist before
			// creating a file.
			dirName := filepath.Dir(header.Name)
			if _, err := os.Stat(filepath.Join(dstDir, dirName)); os.IsNotExist(err) {
				// 0755:
				//    user:   read/write/execute
				//    group:  read/execute
				//    others: read/execute
				os.MkdirAll(filepath.Join(dstDir, dirName), 0o755)
			}
			outFile, err := os.OpenFile(filepath.Join(dstDir, header.Name),
				os.O_CREATE|os.O_WRONLY, header.FileInfo().Mode().Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
		case tar.TypeSymlink:
			if err := os.Symlink(header.Linkname, filepath.Join(dstDir, header.Name)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown type: %b in %s", header.Typeflag, header.Name)
		}

	}
	return nil
}
