// Package archive packs snap/xlog files and a manifest fragment into a
// .tar.zst archive and unpacks it.
package archive

import (
	"archive/tar"
	"errors"
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

// The extensions a file's kind is read off. A file Tarantool is still writing
// carries inProgressExt on top of the extension of its finished form, so the
// suffix is stripped before the kind is read.
const (
	snapExt       = ".snap"
	sortDataExt   = ".sortdata"
	xlogExt       = ".xlog"
	inProgressExt = ".inprogress"
)

// Kind is the kind of data file Tarantool keeps, which is what decides the
// directory a file belongs in: each kind is written into one directory only,
// and a restore has to put every file back into the directory its kind is
// read from.
type Kind int

const (
	// KindVinyl is the vinyl metadata log and the per-space run and index
	// trees, all of which live in vinyl_dir. It is also what a file of no
	// recognized kind counts as, so that neither a backup nor a restore drops
	// a file it cannot name.
	KindVinyl Kind = iota
	// KindSnapshot is a memtx snapshot and the sort data file written beside
	// it, both of which live in memtx_dir.
	KindSnapshot
	// KindWAL is a write-ahead log, which lives in wal_dir.
	KindWAL
)

// KindOf reads a file's kind off its name. Packing and unpacking both classify
// by it, and they have to agree: a file packed as one kind and unpacked as
// another lands in a directory the instance does not read it from.
func KindOf(name string) Kind {
	switch filepath.Ext(strings.TrimSuffix(name, inProgressExt)) {
	case snapExt, sortDataExt:
		return KindSnapshot
	case xlogExt:
		return KindWAL
	default:
		return KindVinyl
	}
}

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

// DataDirs are an instance's data directories, one per kind of file Tarantool
// stores: snapshots in Memtx, write-ahead logs in WAL, and the vinyl metadata
// log together with the per-space run/index trees in Vinyl. An empty field
// means that directory is unknown.
type DataDirs struct {
	WAL   string
	Memtx string
	Vinyl string
}

// rootFor returns the directory holding files of the same kind as file:
// memtx_dir for a snapshot and the sort data beside it, wal_dir for a
// write-ahead log, vinyl_dir for everything else. It reads the kind off the
// name the same way unpacking does, so that a file Tarantool was still writing
// when the backup was taken -- <name>.<ext>.inprogress -- is named against the
// directory its finished form lives in rather than against the vinyl one.
func (dirs DataDirs) rootFor(file string) string {
	switch KindOf(file) {
	case KindSnapshot:
		return dirs.Memtx
	case KindWAL:
		return dirs.WAL
	default:
		return dirs.Vinyl
	}
}

// Pack packs files into dst as a .tar.zst archive, naming each entry with
// EntryName against dirs. At most one DataDirs is meaningful and the first one
// given is used; with none, every file is stored under its base name.
func Pack(dst string, files []string, level int, dirs ...DataDirs) (err error) {
	var roots DataDirs
	if len(dirs) > 0 {
		roots = dirs[0]
	}

	ordered := slices.Clone(files)
	sortWalFiles(ordered)

	names := make([]string, len(ordered))
	for i, file := range ordered {
		names[i] = EntryName(file, roots)
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

// EntryName returns the name file gets inside an archive packed with dirs: its
// path relative to the data directory holding files of its kind, or its base
// name when that directory is unknown or does not contain the file.
//
// A file is never named relative to a directory of another kind, even when that
// directory contains it. Unpacking routes an entry to a target directory by the
// same file kind, so a vinyl run named against a wal_dir that happens to hold
// vinyl_dir would be restored one level too deep under the target vinyl_dir.
// The base name is what is left when the file lies under no directory of its
// own kind: it says nothing about where inside that directory the file sat --
// a vinyl run flattened this way loses the <space_id>/<index_id>/ pair it is
// indexed by -- and is chosen only because it cannot place the file anywhere
// but in the one directory that could hold it.
func EntryName(file string, dirs DataDirs) string {
	root := dirs.rootFor(file)
	if root == "" {
		return filepath.Base(file)
	}

	rel, err := filepath.Rel(root, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Base(file)
	}

	return filepath.ToSlash(rel)
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

// ErrUnsafeEntryName is what CheckEntryName refuses a name with, and through it
// every reader of an archive. It is a sentinel so that a consumer can tell an
// archive naming a file it cannot place -- something about the archive, which
// the caller has to answer for -- from a failure to read the archive at all.
var ErrUnsafeEntryName = errors.New("unsafe archive entry name")

// CheckEntryName rejects any archive entry name that could write outside the
// directory it is extracted into once its parent directories are created, or
// that is not already in canonical form: absolute paths, ".." traversal, "./"
// prefixes, redundant separators and (on Windows) reserved names.
//
// It is exported because a consumer that joins an entry name onto a directory
// of its own answers for the same question, and must not have to restate the
// rule.
func CheckEntryName(name string) error {
	local := filepath.FromSlash(name)
	if !filepath.IsLocal(local) || filepath.Clean(local) != local {
		return fmt.Errorf("%w %q", ErrUnsafeEntryName, name)
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
		return false, CheckEntryName(header.Name) //nolint:wrapcheck
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
