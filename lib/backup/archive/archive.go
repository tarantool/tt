// Package archive packs snap/xlog files and a manifest fragment into a
// .tar.zst archive and unpacks it.
package archive

import (
	"archive/tar"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/klauspost/compress/zstd"

	"github.com/tarantool/tt/cli/util"
)

const (
	snapSuffix = ".snap"
	xlogSuffix = ".xlog"
)

// Entry is a single record inside an archive.
type Entry struct {
	Name string
	Size int64
	Body io.Reader
}

// Pack packs files into dst as a flat .tar.zst archive.
func Pack(dst string, files []string, level int) error {
	ordered := slices.Clone(files)
	sortWalFiles(ordered)

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create archive %q: %w", dst, err)
	}
	defer out.Close()

	zw, err := zstd.NewWriter(out, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)))
	if err != nil {
		return fmt.Errorf("failed to create zstd writer: %w", err)
	}
	defer zw.Close()

	tw := tar.NewWriter(zw)
	defer tw.Close()

	for _, file := range ordered {
		if err := writeFile(tw, file); err != nil {
			return fmt.Errorf("failed to pack %q: %w", file, err)
		}
	}

	// Close explicitly so flushing errors are reported.
	if err := tw.Close(); err != nil {
		return fmt.Errorf("failed to finalize tar stream: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("failed to finalize zstd stream: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("failed to close archive %q: %w", dst, err)
	}
	return nil
}

// sortWalFiles orders .snap before .xlog, then lexicographically by base name.
func sortWalFiles(files []string) {
	hasExt := func(f, s string) bool {
		return strings.HasSuffix(f, s) && len(f) > len(s)
	}
	slices.SortFunc(files, func(left, right string) int {
		if hasExt(left, snapSuffix) && hasExt(right, xlogSuffix) {
			return -1
		}
		if hasExt(left, xlogSuffix) && hasExt(right, snapSuffix) {
			return 1
		}
		return strings.Compare(filepath.Base(left), filepath.Base(right))
	})
}

// writeFile adds a single file to the tar writer under its base name.
func writeFile(tw *tar.Writer, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat: %w", err)
	}

	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return fmt.Errorf("failed to build tar header: %w", err)
	}

	header.Name = filepath.Base(path)

	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("failed to write tar header: %w", err)
	}

	if _, err := io.Copy(tw, file); err != nil {
		return fmt.Errorf("failed to write content: %w", err)
	}

	return nil
}

// Unpack extracts the .tar.zst archive src into destDir.
func Unpack(src, destDir string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open archive %q: %w", src, err)
	}
	defer in.Close()

	zr, err := zstd.NewReader(in)
	if err != nil {
		return fmt.Errorf("failed to create zstd reader: %w", err)
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar entry: %w", err)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
			if err := extractFile(tr, header, destDir); err != nil {
				return fmt.Errorf("failed to extract %q: %w", header.Name, err)
			}
		default:
			return fmt.Errorf("unsupported tar entry %q (type %d)", header.Name, header.Typeflag)
		}
	}
	return nil
}

// extractFile writes one tar entry into destDir, creating parent dirs.
func extractFile(tr *tar.Reader, header *tar.Header, destDir string) error {
	target := filepath.Join(destDir, header.Name)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		header.FileInfo().Mode().Perm())
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	if _, err := io.Copy(out, tr); err != nil {
		out.Close()
		return fmt.Errorf("failed to write content: %w", err)
	}

	if err := out.Close(); err != nil {
		return fmt.Errorf("failed to close file: %w", err)
	}

	return nil
}

// Entries iterates over the archive src yielding one Entry at a time.
func Entries(src string) iter.Seq2[Entry, error] {
	return func(yield func(Entry, error) bool) {
		in, err := os.Open(src)
		if err != nil {
			yield(Entry{}, fmt.Errorf("failed to open archive %q: %w", src, err))
			return
		}
		defer in.Close()

		zr, err := zstd.NewReader(in)
		if err != nil {
			yield(Entry{}, fmt.Errorf("failed to create zstd reader: %w", err))
			return
		}
		defer zr.Close()

		tr := tar.NewReader(zr)
		for {
			header, err := tr.Next()
			if err == io.EOF {
				return
			}
			if err != nil {
				yield(Entry{}, fmt.Errorf("failed to read tar entry: %w", err))
				return
			}

			if header.Typeflag != tar.TypeReg {
				continue
			}

			entry := Entry{
				Name: header.Name,
				Size: header.Size,
				Body: tr,
			}
			if !yield(entry, nil) {
				return
			}
		}
	}
}

// Checksum returns the sha256 of the file at path in hex form.
func Checksum(path string) (string, error) {
	sum, err := util.FileSHA256Hex(path)
	if err != nil {
		return "", fmt.Errorf("failed to checksum %q: %w", path, err)
	}
	return sum, nil
}
