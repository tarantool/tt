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

// Entry is a single record inside an archive.
type Entry struct {
	Name string
	Size int64
	// Body streams the entry's content. It is backed by the shared archive
	// reader and is only valid until the next iteration step: read what you
	// need of it before continuing the range loop. Do not retain it or read it
	// after the loop advances — it will then point at another entry or a closed
	// reader. Leaving a body unread, whole or in part, is allowed: the iterator
	// skips the remainder before yielding the next entry, which is what a
	// consumer wanting only the entry names relies on.
	Body io.Reader
}

// Pack packs files into dst as a flat .tar.zst archive.
func Pack(dst string, files []string, level int, roots ...string) (err error) {
	ordered := slices.Clone(files)
	sortWalFiles(ordered)

	names := make([]string, len(ordered))
	for i, file := range ordered {
		names[i] = EntryName(file, roots...)
	}

	if err := checkUniqueNames(ordered, names); err != nil {
		return fmt.Errorf("failed to pack %q: %w", dst, err)
	}

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create archive %q: %w", dst, err)
	}
	// Remove the half-written archive on any failure so a caller never mistakes
	// a structurally valid but incomplete backup for a good one.
	defer func() {
		if err != nil {
			out.Close()
			_ = os.Remove(dst)
		}
	}()

	zw, err := zstd.NewWriter(out, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)))
	if err != nil {
		return fmt.Errorf("failed to create zstd writer: %w", err)
	}

	tw := tar.NewWriter(zw)

	for i, file := range ordered {
		if err = writeFile(tw, file, names[i]); err != nil {
			return fmt.Errorf("failed to pack %q: %w", file, err)
		}
	}

	// Close explicitly so flushing errors are reported.
	if err = tw.Close(); err != nil {
		return fmt.Errorf("failed to finalize tar stream: %w", err)
	}
	if err = zw.Close(); err != nil {
		return fmt.Errorf("failed to finalize zstd stream: %w", err)
	}
	if err = out.Sync(); err != nil {
		return fmt.Errorf("failed to sync archive %q: %w", dst, err)
	}
	if err = out.Close(); err != nil {
		return fmt.Errorf("failed to close archive %q: %w", dst, err)
	}
	return nil
}

// EntryName returns the name file gets inside an archive packed with roots:
// its path relative to whichever of roots contains it, or its base name when
// none does (or none are given).
func EntryName(file string, roots ...string) string {
	for _, root := range roots {
		if root == "" {
			continue
		}

		rel, err := filepath.Rel(root, file)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}

		return filepath.ToSlash(rel)
	}

	return filepath.Base(file)
}

// checkUniqueNames rejects inputs that resolve to the same archive entry
// name.
func checkUniqueNames(files, names []string) error {
	seen := make(map[string]string, len(names))
	for i, name := range names {
		if prev, ok := seen[name]; ok {
			return fmt.Errorf("duplicate archive entry name %q (from %q and %q)",
				name, prev, files[i])
		}

		seen[name] = files[i]
	}

	return nil
}

// sortWalFiles orders files by LSN, i.e. by base name. Tarantool snap/xlog
// names are fixed-width zero-padded LSNs, so lexicographic order equals numeric
// LSN order: a snapshot and the WAL that continues it interleave correctly
// (e.g. snap N, xlog N, xlog N+…, snap M, xlog M), and a snap and xlog sharing
// an LSN order snap-before-xlog for free because ".snap" < ".xlog". This is a
// valid total order; non-wal files (e.g. the manifest fragment) sort by name.
func sortWalFiles(files []string) {
	slices.SortFunc(files, func(left, right string) int {
		return strings.Compare(filepath.Base(left), filepath.Base(right))
	})
}

// writeFile adds a single file to the tar writer under the given entry name.
func writeFile(tw *tar.Writer, path, name string) error {
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

	header.Name = name

	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("failed to write tar header: %w", err)
	}

	if _, err := io.Copy(tw, file); err != nil {
		return fmt.Errorf("failed to write content: %w", err)
	}

	return nil
}

// ensureSafeEntryName rejects any archive entry name that could write outside
// destDir once its parent directories are created, or that is not already in
// canonical form: absolute paths, ".." traversal, "./" prefixes, redundant
// separators and (on Windows) reserved names.
func ensureSafeEntryName(name string) error {
	local := filepath.FromSlash(name)
	if !filepath.IsLocal(local) || filepath.Clean(local) != local {
		return fmt.Errorf("unsafe archive entry name %q", name)
	}

	return nil
}

// checkEntry validates a tar header for both readers. A directory entry carries
// no content and is skipped; any other non-regular entry is rejected, so an
// archive holding a symlink where a journal belongs can never be read as a
// shorter but healthy one.
func checkEntry(header *tar.Header) (skip bool, err error) {
	switch header.Typeflag {
	case tar.TypeDir:
		return true, nil
	case tar.TypeReg:
		return false, ensureSafeEntryName(header.Name) //nolint:wrapcheck
	default:
		return false, fmt.Errorf("unsupported tar entry %q (type %d)", header.Name, header.Typeflag)
	}
}

// openArchive opens src and returns a tar reader over its zstd-decompressed
// contents plus a close function that releases the zstd reader and the file.
func openArchive(src string) (*tar.Reader, func() error, error) {
	in, err := os.Open(src)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open archive %q: %w", src, err)
	}

	zr, err := zstd.NewReader(in)
	if err != nil {
		in.Close()
		return nil, nil, fmt.Errorf("failed to create zstd reader: %w", err)
	}

	closeArchive := func() error {
		zr.Close()
		return in.Close()
	}
	return tar.NewReader(zr), closeArchive, nil
}

// Unpack extracts the .tar.zst archive src into destDir.
func Unpack(src, destDir string) error {
	tr, closeArchive, err := openArchive(src)
	if err != nil {
		return fmt.Errorf("failed to unpack %q: %w", src, err)
	}
	defer closeArchive()

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar entry: %w", err)
		}

		skip, err := checkEntry(header)
		if err != nil {
			return err //nolint:wrapcheck
		}
		if skip {
			continue
		}

		if err := extractFile(tr, header, destDir); err != nil {
			return fmt.Errorf("failed to extract %q: %w", header.Name, err)
		}
	}
	return nil
}

// extractFile writes one tar entry into destDir, which it creates if missing,
// along with any subdirectories the entry's (already validated, safe) name
// carries -- vinyl's .run/.index files land under <space_id>/<index_id>/.
func extractFile(tr *tar.Reader, header *tar.Header, destDir string) error {
	target := filepath.Join(destDir, filepath.FromSlash(header.Name))
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
		tr, closeArchive, err := openArchive(src)
		if err != nil {
			yield(Entry{}, err)
			return
		}
		defer closeArchive()

		for {
			header, err := tr.Next()
			if err == io.EOF {
				return
			}
			if err != nil {
				yield(Entry{}, fmt.Errorf("failed to read tar entry: %w", err))
				return
			}

			skip, err := checkEntry(header)
			if err != nil {
				yield(Entry{}, err)
				return
			}
			if skip {
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
