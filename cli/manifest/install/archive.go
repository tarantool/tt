package install

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/util"
)

// File modes install writes with. Directories and executables get 0755, plain
// files 0644 — the same two modes the pack writer normalized every entry to.
const (
	dirPerm  os.FileMode = 0o755
	execPerm os.FileMode = 0o755
	filePerm os.FileMode = 0o644
	// execBit is the owner-executable bit tested to tell the two modes apart.
	execBit = 0o100
)

// Archive is a read handle on a .tt package archive (tar+zstd). It is the read
// counterpart of cli/manifest/pack's write-only archive writer: pack never
// produced a reader, so install carries its own. Each method opens the file
// afresh, so an Archive is cheap to hold and safe to reuse.
type Archive struct {
	path string
}

// OpenArchive returns a read handle on the archive at path. It only checks that
// the file exists; the tar+zstd stream is validated lazily on the first read.
func OpenArchive(archivePath string) (*Archive, error) {
	_, err := os.Stat(archivePath)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errBadArchive, err)
	}

	return &Archive{path: archivePath}, nil
}

// Path is the archive's filesystem path.
func (a *Archive) Path() string { return a.path }

// SHA256 returns the archive's checksum, lowercase hex — the same value
// tt package pack reported when it wrote the file.
func (a *Archive) SHA256() (string, error) {
	sum, err := util.FileSHA256Hex(a.path)
	if err != nil {
		return "", fmt.Errorf("checksumming archive: %w", err)
	}

	return sum, nil
}

// Header is the tt-owned metadata read from an archive without extracting it:
// the manifest, the lock and the declared version, plus whether the archive
// bundles a runtime (a with-deps archive) — the facts install needs to decide
// scope compatibility, collisions and reconciliation before touching disk.
type Header struct {
	// Manifest is the parsed app.manifest.toml.
	Manifest *manifest.Manifest
	// ManifestBytes is its raw bytes, kept for the --locked hash check.
	ManifestBytes []byte
	// Lock is the parsed app.manifest.lock.
	Lock *manifest.Lock
	// Version is the VERSION file's content, trimmed.
	Version string
	// WithDeps reports whether the archive carries _runtime/ and the full
	// dependency closure (the default pack mode). A --without-deps archive has
	// neither, and installing it refetches the closure from the registry.
	WithDeps bool
}

// ReadHeader scans the archive once, reading the three tt-owned metadata members
// into memory and noting whether a _runtime/ tree is present. It never writes to
// disk, so a scope or runtime rejection happens before any extraction.
func (a *Archive) ReadHeader() (*Header, error) {
	manifestBytes, lockBytes, versionBytes, hasRuntime, err := a.readMetaMembers()
	if err != nil {
		return nil, err
	}

	if manifestBytes == nil {
		return nil, fmt.Errorf("%w: missing %s", errBadArchive, manifestFileName)
	}

	if lockBytes == nil {
		return nil, fmt.Errorf("%w: missing %s", errBadArchive, lockFileName)
	}

	man, _, err := manifest.ParseManifest(manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errBadArchive, err)
	}

	lock, err := manifest.ParseLock(lockBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errBadArchive, err)
	}

	return &Header{
		Manifest:      man,
		ManifestBytes: manifestBytes,
		Lock:          lock,
		Version:       strings.TrimSpace(string(versionBytes)),
		WithDeps:      hasRuntime,
	}, nil
}

// Extract writes archive entries into dstDir. For each entry mapEntry is given
// the entry's slash-path and returns the destination path relative to dstDir and
// whether to write it at all; returning ok=false skips the entry. This lets a
// caller both filter (skip a dependency's subtree) and remap (strip the .rocks/
// prefix for a user-scope tree). Every resolved destination is validated to stay
// within dstDir; an escaping entry is a hard error and nothing more is written.
func (a *Archive) Extract(dstDir string, mapEntry func(name string) (string, bool)) error {
	err := os.MkdirAll(dstDir, dirPerm)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dstDir, err)
	}

	return a.walk(func(hdr *tar.Header, reader io.Reader) (bool, error) {
		name := path.Clean(filepath.ToSlash(hdr.Name))

		rel, ok := name, true
		if mapEntry != nil {
			rel, ok = mapEntry(name)
		}

		if !ok || rel == "" {
			return true, nil
		}

		dst, err := safeJoin(dstDir, rel)
		if err != nil {
			return false, err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			return true, mkdirWrapped(dst)
		case tar.TypeReg:
			return true, writeFile(dst, reader, entryMode(hdr))
		default:
			// The pack writer emits only files and directories; anything else is
			// skipped rather than trusted.
			return true, nil
		}
	})
}

// readMetaMembers scans the archive once and returns the raw bytes of the three
// tt-owned metadata members plus whether a _runtime/ tree is present.
func (a *Archive) readMetaMembers() ([]byte, []byte, []byte, bool, error) {
	var (
		manifestBytes []byte
		lockBytes     []byte
		versionBytes  []byte
		hasRuntime    bool
	)

	err := a.walk(func(hdr *tar.Header, reader io.Reader) (bool, error) {
		name := path.Clean(filepath.ToSlash(hdr.Name))

		if top, _, _ := strings.Cut(name, "/"); top == runtimeDirName {
			hasRuntime = true
		}

		if hdr.Typeflag != tar.TypeReg {
			return true, nil
		}

		data, readErr := io.ReadAll(reader)
		if readErr != nil {
			return false, fmt.Errorf("%w: reading %s: %w", errBadArchive, name, readErr)
		}

		switch name {
		case manifestFileName:
			manifestBytes = data
		case lockFileName:
			lockBytes = data
		case versionFileName:
			versionBytes = data
		}

		return true, nil
	})
	if err != nil {
		return nil, nil, nil, false, err
	}

	return manifestBytes, lockBytes, versionBytes, hasRuntime, nil
}

// walk opens the archive, decompresses it and calls visit for each tar entry.
// visit returns whether to continue and an error that stops the walk. The reader
// passed to visit is valid only for that call.
func (a *Archive) walk(visit func(*tar.Header, io.Reader) (bool, error)) error {
	file, err := os.Open(a.path)
	if err != nil {
		return fmt.Errorf("%w: %w", errBadArchive, err)
	}

	defer func() { _ = file.Close() }()

	zstdReader, err := zstd.NewReader(file)
	if err != nil {
		return fmt.Errorf("%w: opening zstd stream: %w", errBadArchive, err)
	}

	defer zstdReader.Close()

	tarReader := tar.NewReader(zstdReader)

	for {
		hdr, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("%w: reading tar stream: %w", errBadArchive, err)
		}

		cont, err := visit(hdr, tarReader)
		if err != nil {
			return err
		}

		if !cont {
			return nil
		}
	}
}

// entryMode is the file mode an archive entry is written with: 0755 when the
// stored mode carries the executable bit (runtime binaries, scripts), 0644
// otherwise. The pack writer already normalized modes to exactly these two.
func entryMode(hdr *tar.Header) os.FileMode {
	if hdr.Mode&execBit != 0 {
		return execPerm
	}

	return filePerm
}

// mkdirWrapped creates dir and its parents, wrapping the external error.
func mkdirWrapped(dir string) error {
	err := os.MkdirAll(dir, dirPerm)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	return nil
}

// writeFile creates dst (and its parents) and copies reader into it with mode
// perm.
func writeFile(dst string, reader io.Reader, perm os.FileMode) error {
	err := mkdirWrapped(filepath.Dir(dst))
	if err != nil {
		return err
	}

	// Path is validated by safeJoin; the archive is the user's own package.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm) //nolint:gosec
	if err != nil {
		return fmt.Errorf("creating %s: %w", dst, err)
	}

	_, err = io.Copy(out, reader)
	if err != nil {
		_ = out.Close()

		return fmt.Errorf("writing %s: %w", dst, err)
	}

	err = out.Close()
	if err != nil {
		return fmt.Errorf("closing %s: %w", dst, err)
	}

	return nil
}

// safeJoin joins a slash-separated archive entry name onto dstDir, refusing any
// name that is absolute or climbs out of dstDir (a tar-slip attempt).
func safeJoin(dstDir, name string) (string, error) {
	if name == "" || name == "." {
		return dstDir, nil
	}

	if path.IsAbs(name) || strings.HasPrefix(name, "../") ||
		name == ".." || strings.Contains(name, "/../") {
		return "", fmt.Errorf("%w: %q", errUnsafePath, name)
	}

	joined := filepath.Join(dstDir, filepath.FromSlash(name))

	rel, err := filepath.Rel(dstDir, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: %q", errUnsafePath, name)
	}

	return joined, nil
}
