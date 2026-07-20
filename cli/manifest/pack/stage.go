package pack

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/cli/manifest"
)

// Staging tree modes.
const (
	filePerm os.FileMode = 0o644
	dirPerm  os.FileMode = 0o755
)

// stage assembles the archive tree under stageDir: the manifest, lock, VERSION,
// license files, [package].include payload and the .rocks/ subtree, filtered by
// packing mode. _runtime/ is added separately by bundleRuntime.
//
// Everything is copied into a staging directory rather than streamed straight
// from the project, so the tar layer sees one flat, already-correct tree and
// the mode filtering happens exactly once, in one place.
func stage(stageDir string, req stageRequest) error {
	if err := os.MkdirAll(stageDir, dirPerm); err != nil {
		return fmt.Errorf("creating staging directory: %w", err)
	}

	if err := stageMetadata(stageDir, req); err != nil {
		return err
	}

	if err := stagePayload(stageDir, req); err != nil {
		return err
	}

	return stageRocks(stageDir, req)
}

// stageRequest carries everything stage needs to lay out the archive tree.
type stageRequest struct {
	// ProjectDir is the source project root.
	ProjectDir string
	// Manifest is the parsed manifest; its raw bytes go into the archive as-is.
	Manifest *manifest.Manifest
	// LockBytes is the marshaled lock, already carrying the bundled_* versions.
	LockBytes []byte
	// Version is the derived version string written to VERSION.
	Version string
	// Tree is the project's materialized .rocks/ directory.
	Tree string
	// WithDeps keeps foreign dependencies in .rocks/; false strips everything
	// but the package's own namespace subtrees.
	WithDeps bool
	// Namespaces lists the rocks-tree namespaces the package itself owns. In
	// --without-deps mode only these survive under share/ and lib/.
	Namespaces []string
	// HasFlatNamespace records that some component declares namespace = "",
	// which --without-deps cannot express (see stageRocks).
	HasFlatNamespace bool
}

// stageMetadata writes the three tt-owned metadata files.
func stageMetadata(stageDir string, req stageRequest) error {
	files := []struct {
		name string
		data []byte
	}{
		{manifestFileName, req.Manifest.Raw()},
		{lockFileName, req.LockBytes},
		{versionFileName, []byte(req.Version + "\n")},
	}

	for _, f := range files {
		path := filepath.Join(stageDir, f.name)
		if err := os.WriteFile(path, f.data, filePerm); err != nil {
			return fmt.Errorf("staging %s: %w", f.name, err)
		}
	}

	return nil
}

// stagePayload copies the license files and the [package].include entries into
// the archive root, keeping each entry's path relative to the project.
func stagePayload(stageDir string, req stageRequest) error {
	pkg := req.Manifest.Package

	for _, entry := range pkg.LicenseFiles {
		if err := stageEntry(stageDir, req.ProjectDir, entry, "license_files"); err != nil {
			return err
		}
	}

	for _, entry := range pkg.Include {
		if err := stageEntry(stageDir, req.ProjectDir, entry, "include"); err != nil {
			return err
		}
	}

	return nil
}

// stageEntry copies one manifest-declared path into the staging tree. The entry
// is rejected if it escapes the project, collides with a reserved archive name
// or matches nothing — a silently dropped LICENSE is worse than a failed pack.
func stageEntry(stageDir, projectDir, entry, field string) error {
	clean := filepath.Clean(entry)
	upward := clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator))

	if filepath.IsAbs(clean) || upward {
		return stateErrorf("%s entry %q: %w", field, entry, errEscapingPath)
	}

	if isReservedName(clean) {
		return stateErrorf("%s entry %q: %w", field, entry, errReservedName)
	}

	src := filepath.Join(projectDir, clean)

	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return stateErrorf("%s entry %q: %w", field, entry, errMissingInclude)
		}

		return fmt.Errorf("%s entry %q: %w", field, entry, err)
	}

	dst := filepath.Join(stageDir, clean)

	if info.IsDir() {
		return copyTree(src, dst)
	}

	return copyFile(src, dst)
}

// stageRocks copies the project's .rocks/ tree into the archive. In --with-deps
// mode the tree is copied whole; in --without-deps mode only the package's own
// namespace subtrees under share/tarantool/ and lib/tarantool/ survive, which
// is what leaves the dependency closure to be refetched from the lock at
// install time.
func stageRocks(stageDir string, req stageRequest) error {
	if _, err := os.Stat(req.Tree); os.IsNotExist(err) {
		// A pure-metadata package need not have produced a tree.
		return nil
	}

	dstRocks := filepath.Join(stageDir, rocksDirName)

	if req.WithDeps {
		return copyTree(req.Tree, dstRocks)
	}

	if req.HasFlatNamespace {
		// A flat namespace (namespace = "") owns no subtree: its files sit
		// directly among the tree roots, indistinguishable by path from a
		// dependency's. Dropping them silently would lose the package's own code
		// from an archive whose whole promise is to keep exactly that, so this is
		// refused rather than quietly under-packing.
		return stateErrorf(
			"%w: a component with namespace = \"\" lays its files flat in the "+
				"rocks tree, where they cannot be told apart from dependencies; "+
				"pack it with the default --with-deps instead",
			errFlatNamespace)
	}

	for _, sub := range []string{shareTarantool, libTarantool} {
		for _, ns := range req.Namespaces {
			if ns == "" {
				continue
			}

			src := filepath.Join(req.Tree, filepath.FromSlash(sub), ns)
			if _, err := os.Stat(src); os.IsNotExist(err) {
				continue
			}

			dst := filepath.Join(dstRocks, filepath.FromSlash(sub), ns)
			if err := copyTree(src, dst); err != nil {
				return err
			}
		}
	}

	return nil
}

// Rocks-tree layout roots, mirroring cli/manifest/build.
const (
	shareTarantool = "share/tarantool"
	libTarantool   = "lib/tarantool"
)

// copyTree recursively copies src to dst, creating parents as needed. The
// archive carries no link structure, so symlinks are dereferenced: a link to a
// file is copied as that file, a link to a directory is descended into.
//
// The root is resolved up front because WalkDir does not follow a symlinked
// root - it reports it as a plain symlink entry, which would otherwise send a
// directory down the file-copy path. Package prefixes hit this routinely
// (Homebrew's share/tarantool is a link into the Cellar).
func copyTree(src, dst string) error {
	if resolved, err := filepath.EvalSymlinks(src); err == nil {
		src = resolved
	}

	// Refuse to copy a tree into itself. The staging directory lives inside the
	// project (_build/pack/stage-*), so a payload entry naming an ancestor of it
	// would have WalkDir descend into the copies it is creating, recursing until
	// the path outgrows PATH_MAX. isReservedName rejects the entries that reach
	// this today; the guard keeps the invariant independent of that list.
	if within(dst, src) {
		return stateErrorf("refusing to copy %s into its own subdirectory %s", src, dst)
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, dirPerm)
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		if info.Mode()&os.ModeSymlink != 0 {
			// Stat follows the link; a link to a directory becomes its own walk.
			resolved, err := os.Stat(path)
			if err != nil {
				// A dangling link is skipped rather than failing the pack.
				return nil //nolint:nilerr // A dangling symlink is not packable.
			}

			if resolved.IsDir() {
				return copyTree(path, target)
			}

			return copyFile(path, target)
		}

		if !info.Mode().IsRegular() {
			// Sockets, devices and pipes have no place in an archive.
			return nil
		}

		return copyFile(path, target)
	})
}

// within reports whether path is inside root (or is root itself). Both sides
// are resolved first: on macOS EvalSymlinks turns /var into /private/var, so
// comparing a resolved root against an unresolved path finds no relation even
// when one contains the other.
func within(path, root string) bool {
	rel, err := filepath.Rel(resolveExisting(root), resolveExisting(path))
	if err != nil {
		return false
	}

	return rel == "." || !strings.HasPrefix(rel, "..")
}

// resolveExisting resolves symlinks in the longest existing prefix of path and
// re-appends the remainder, so a path that does not exist yet (the staging
// directory) still normalizes the same way as one that does.
func resolveExisting(path string) string {
	path = filepath.Clean(path)

	rest := ""
	for cur := path; ; {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(resolved, rest)
		}

		parent := filepath.Dir(cur)
		if parent == cur {
			return path
		}

		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// copyFile copies one file, creating the destination's parent and preserving
// the executable bit (which _runtime/ binaries depend on).
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return err
	}

	// Stat, not Lstat: a symlinked binary in bin_dir must be copied as its
	// target, since the archive carries no link structure.
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	perm := filePerm
	if info.Mode().Perm()&0o100 != 0 {
		perm = 0o755
	}

	in, err := os.Open(src) //nolint:gosec // Sources are project or runtime files.
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	//nolint:gosec // The destination is inside our own staging tree.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()

		return fmt.Errorf("copying %s: %w", src, err)
	}

	return out.Close()
}
