package pack

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/tarantool/tt/cli/util"
)

// Archive entry modes. Reproducibility beats preserving the source tree's
// permissions, so every entry is normalized to one of two modes; the only bit
// that survives from disk is the executable bit, which _runtime/ binaries and
// component scripts need.
const (
	archiveFileMode = 0o644
	archiveExecMode = 0o755
	archiveDirMode  = 0o755
)

// archiveModTime is the timestamp stamped on every entry. The Unix epoch is the
// usual reproducible-build choice; the zero time.Time is year 1, which tar
// stores as a large negative stamp that some extractors reject.
var archiveModTime = time.Unix(0, 0).UTC()

// writeArchive packs the staging directory into destPath as tar+zstd and
// returns the archive's sha256, lowercase hex.
//
// The archive is reproducible: identical staged content yields a byte-identical
// file. That rules out cli/pack.WriteTarArchive, whose tar.FileInfoHeader
// stamps each entry's real mtime, uid and gid — three inputs that change
// between runs on the same content. Here every header is normalized (epoch
// mtime, root:root, fixed mode) and entries are emitted in lexical order, which
// filepath.WalkDir already guarantees.
func writeArchive(stageDir, destPath string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(destPath), dirPerm); err != nil {
		return "", fmt.Errorf("creating archive directory: %w", err)
	}

	destFile, err := os.Create(destPath) //nolint:gosec // Path is derived from the manifest.
	if err != nil {
		return "", fmt.Errorf("creating archive: %w", err)
	}

	// Close errors matter on the write path: a swallowed zstd or tar Close
	// silently truncates the frame and yields a corrupt archive that only fails
	// at install time. The successful path checks every close; the failure paths
	// discard them, since the original error already explains the failure and
	// removing the partial archive is best-effort cleanup.
	if err := writeArchiveTo(stageDir, destFile); err != nil {
		_ = destFile.Close()
		_ = os.Remove(destPath)

		return "", err
	}

	if err := destFile.Close(); err != nil {
		_ = os.Remove(destPath)

		return "", fmt.Errorf("closing archive: %w", err)
	}

	sum, err := util.FileSHA256Hex(destPath)
	if err != nil {
		return "", fmt.Errorf("checksumming archive: %w", err)
	}

	return sum, nil
}

// writeArchiveTo streams stageDir into w as tar+zstd, closing both the tar and
// the zstd writer in the right order (tar first, so its trailer is compressed).
func writeArchiveTo(stageDir string, w io.Writer) error {
	zw, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return fmt.Errorf("creating zstd writer: %w", err)
	}

	tw := tar.NewWriter(zw)

	if err := walkIntoTar(stageDir, tw); err != nil {
		// Both closes are best-effort here; the walk error is the real one.
		_ = tw.Close()
		_ = zw.Close()

		return err
	}

	if err := tw.Close(); err != nil {
		_ = zw.Close()

		return fmt.Errorf("closing tar stream: %w", err)
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("closing zstd stream: %w", err)
	}

	return nil
}

// walkIntoTar writes every entry under root into tw with a normalized header.
func walkIntoTar(root string, tw *tar.Writer) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		if rel == "." {
			return nil
		}

		// Archive paths are slash-separated regardless of host separator.
		name := filepath.ToSlash(rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		switch {
		case d.IsDir():
			return tw.WriteHeader(dirHeader(name))
		case info.Mode().IsRegular():
			return writeRegular(tw, path, name, info)
		default:
			// Symlinks and everything else are skipped: the staging tree is
			// assembled by copying file contents, so nothing else should appear.
			return nil
		}
	})
}

// dirHeader builds a normalized header for a directory entry.
func dirHeader(name string) *tar.Header {
	return &tar.Header{
		Typeflag: tar.TypeDir,
		Name:     name + "/",
		Mode:     archiveDirMode,
		ModTime:  archiveModTime,
		Format:   tar.FormatPAX,
	}
}

// writeRegular writes one regular file's header and contents.
func writeRegular(tw *tar.Writer, path, name string, info fs.FileInfo) error {
	mode := int64(archiveFileMode)
	if info.Mode().Perm()&0o100 != 0 {
		mode = archiveExecMode
	}

	header := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     name,
		Mode:     mode,
		Size:     info.Size(),
		ModTime:  archiveModTime,
		Format:   tar.FormatPAX,
	}

	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	src, err := os.Open(path) //nolint:gosec // Path comes from our own staging tree.
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	if _, err := io.Copy(tw, src); err != nil {
		return fmt.Errorf("archiving %s: %w", name, err)
	}

	return nil
}

// isReservedName reports whether a path inside the archive would collide with a
// name tt owns. The check is on the first path segment, so both "VERSION" and
// ".rocks/share/..." are caught.
//
// The comparison is case-insensitive because the staging tree is a real
// directory on the host filesystem, and on a case-insensitive one (APFS by
// default, NTFS) an include entry named "version" opens the already-staged
// VERSION file and truncates it. Matching only the exact case would let a
// manifest silently overwrite tt's own metadata inside the archive.
//
// "." and ".." are reserved too: "." resolves to the project root, which
// contains the staging directory, and copying it would recurse into itself.
func isReservedName(name string) bool {
	top, _, _ := strings.Cut(filepath.ToSlash(name), "/")

	switch strings.ToLower(top) {
	case manifestFileName, lockFileName, strings.ToLower(versionFileName),
		runtimeDirName, rocksDirName, buildDirName, ".", "..", "":
		return true
	default:
		return false
	}
}
