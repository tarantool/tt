package pack

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/cli/configure"
)

// writeTgzArchive creates TGZ archive of specified path.
func writeTgzArchive(srcDirPath string, destFilePath string, packCtx PackCtx) error {
	destFile, err := os.Create(destFilePath)
	if err != nil {
		return fmt.Errorf("failed to create result TGZ file %s: %s", destFilePath, err)
	}

	gzipWriter := gzip.NewWriter(destFile)
	defer gzipWriter.Close()

	err = WriteTarArchive(srcDirPath, gzipWriter, packCtx.RpmDeb.pkgFilesInfo)
	if err != nil {
		return err
	}

	return nil
}

// WriteTarArchive creates Tar archive of specified path
// using specified writer
func WriteTarArchive(srcDirPath string, compressWriter io.Writer,
	pkgFiles map[string]packFileInfo) error {
	tarWriter := tar.NewWriter(compressWriter)
	defer tarWriter.Close()

	err := filepath.Walk(srcDirPath, func(filePath string, fileInfo os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		var tarHeader *tar.Header
		if fileInfo.Mode().Type() == os.ModeSymlink &&
			strings.Contains(filePath, configure.InstancesEnabledDirName) {
			// If we have found a symlink while making tarball
			// we should make it relative. Apriori it is known,
			// that the source path of the link will be located
			// in a directory higher.
			srcPath, _ := filepath.EvalSymlinks(filePath)
			resolvedPath := filepath.Join("..", filepath.Base(srcPath))
			tarHeader, err = tar.FileInfoHeader(fileInfo, resolvedPath)
			if err != nil {
				return err
			}
		} else {
			tarHeader, err = tar.FileInfoHeader(fileInfo, filePath)
			if err != nil {
				return err
			}
		}

		relPath, err := filepath.Rel(srcDirPath, filePath)
		if err != nil {
			return fmt.Errorf("failed to get relative path of %q: %s", filePath, err)
		}
		if packFileInfo, found := pkgFiles[relPath]; found {
			tarHeader.Uname = packFileInfo.owner
			tarHeader.Gname = packFileInfo.group
		} else {
			tarHeader.Uname = defaultFileUser
			tarHeader.Gname = defaultFileGroup
		}

		tarHeader.Name, err = filepath.Rel(srcDirPath, filePath)
		if err != nil {
			return err
		}

		if err := tarWriter.WriteHeader(tarHeader); err != nil {
			return err
		}

		if fileInfo.Mode().IsRegular() {
			if err := writeFileToWriter(filePath, tarWriter); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// CompressGzip compresses specified file with gzip.BestCompression level.
func CompressGzip(srcFilePath string, destFilePath string) error {
	// Src file reader.
	srcFileReader, err := os.Open(srcFilePath)
	if err != nil {
		return fmt.Errorf("failed to open source file %s: %s", srcFilePath, err)
	}
	defer srcFileReader.Close()

	// Dest file writer.
	destFile, err := os.Create(destFilePath)
	if err != nil {
		return fmt.Errorf("failed to create result GZIP file %s: %s", destFilePath, err)
	}
	defer destFile.Close()

	// Dest file GZIP writer.
	gzipWriter, err := gzip.NewWriterLevel(destFile, gzip.BestCompression)
	if err != nil {
		_ = os.Remove(destFilePath)
		return fmt.Errorf("failed to create GZIP writer %s: %s", destFilePath, err)
	}
	defer gzipWriter.Flush()

	// Compressing itself.
	if _, err := io.Copy(gzipWriter, srcFileReader); err != nil {
		_ = os.Remove(destFilePath)
		return err
	}

	return nil
}

func writeFileToWriter(filePath string, writer io.Writer) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Copy file data into writer.
	if _, err := io.Copy(writer, file); err != nil {
		return err
	}

	return nil
}
