package pack

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// Staging tree modes.
const (
	filePerm os.FileMode = 0o644
	dirPerm  os.FileMode = 0o755
)

// stage assembles the archive tree under stageDir: the manifest, lock, VERSION,
// license files, [package].include payload, the instance templates in
// .templates/ and the .rocks/ subtree, filtered by packing mode. _runtime/ is
// added separately by bundleRuntime.
//
// Everything is copied into a staging directory rather than streamed straight
// from the project, so the tar layer sees one flat, already-correct tree and
// the mode filtering happens exactly once, in one place.
func stage(ctx context.Context, stageDir string, req stageRequest) error {
	err := os.MkdirAll(stageDir, dirPerm)
	if err != nil {
		return fmt.Errorf("creating staging directory: %w", err)
	}

	err = stageMetadata(stageDir, req)
	if err != nil {
		return err
	}

	err = stagePayload(stageDir, req)
	if err != nil {
		return err
	}

	err = stageTemplates(ctx, stageDir, req.ProjectDir)
	if err != nil {
		return err
	}

	return stageRocks(ctx, stageDir, req)
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
	// WithDeps keeps foreign dependencies in .rocks/; false keeps the rock of
	// the package alone.
	WithDeps bool
	// DevOnly lists the locked rocks that only the dev closure brings. They
	// are left out of .rocks/ in both packing modes: they are the
	// developer's tools, and the spec is explicit that they never reach the
	// archive. See devOnlyRocks.
	DevOnly []manifest.LockDependency
	// Warn receives non-fatal diagnostics; nil drops them.
	Warn func(string)
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

		err := os.WriteFile(path, f.data, filePerm)
		if err != nil {
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
		err := stageEntry(stageDir, req.ProjectDir, entry, "license_files")
		if err != nil {
			return err
		}
	}

	for _, entry := range pkg.Include {
		err := stageEntry(stageDir, req.ProjectDir, entry, "include")
		if err != nil {
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

// stageRocks grafts the rocks of the project's .rocks/ tree into the .rocks/
// of the archive: each is staged from the files its rock_manifest lists, and
// checked against it, and installed into the archive's tree as its binary
// rock would be, which records it in the archive's tree manifest (tree.Graft).
// The project tree's manifest is not copied, and neither is a file no rock
// lists there: one is reported, as a file the archive leaves out.
//
// In --with-deps mode every rock goes but those only the dev closure brings;
// in --without-deps mode the rock of the package alone, which holds the
// components, version.lua and the programs of the executable components the
// build installed, and which leaves the dependency closure to be refetched
// from the lock at install time.
//
// The archive's tree has no command wrappers: a command of a rock is its
// script, as the rock packs it, and the install writes the wrapper the tree it
// installs into needs. A wrapper names the tree's root and the interpreter by
// their paths, which would be the build machine's, and would make the archive
// depend on where it was staged.
func stageRocks(ctx context.Context, stageDir string, req stageRequest) error {
	_, err := os.Stat(req.Tree)
	if os.IsNotExist(err) {
		// A pure-metadata package need not have produced a tree.
		return nil
	}

	dstRocks := filepath.Join(stageDir, rocksDirName)

	archiveTree, err := tree.Open(luarocks.Config{Tree: dstRocks})
	if err != nil {
		return fmt.Errorf("staging %s: %w", rocksDirName, err)
	}

	devOnly := make(map[string]bool, len(req.DevOnly))
	for _, rock := range req.DevOnly {
		devOnly[rock.Name] = true
	}

	pkg := req.Manifest.Package.Name

	selectRock := func(ref tree.RockRef) bool {
		if !req.WithDeps {
			return ref.Name == pkg
		}

		return !devOnly[ref.Name]
	}

	var report *tree.GraftReport

	err = archiveTree.Update(ctx, func(change *tree.Tx) error {
		var err error

		report, err = change.Graft(req.Tree, tree.GraftOpts{
			Select: selectRock, Replace: false, RemoveOtherVersions: false,
		})

		return err //nolint:wrapcheck // Wrapped with the Update's error below.
	})
	if err != nil {
		return stateErrorf("staging the rocks of %s: %w", req.Tree, err)
	}

	// The lock the Update took is the archive's no more than the project's
	// manifest is.
	err = os.Remove(filepath.Join(dstRocks, filepath.FromSlash(rocksInstallDir), treeLockFile))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("staging %s: %w", rocksDirName, err)
	}

	warnUntracked(req, report.Untracked)

	return nil
}

// warnUntracked reports the files of the project's tree no rock lists, which
// the archive leaves out: laid there by hand or by another tool, or left by
// an older tt.
func warnUntracked(req stageRequest, untracked []string) {
	if req.Warn == nil || len(untracked) == 0 {
		return
	}

	const shown = 5

	names := make([]string, 0, min(len(untracked), shown))

	for _, path := range untracked[:min(len(untracked), shown)] {
		rel, err := filepath.Rel(req.Tree, path)
		if err != nil {
			rel = path
		}

		names = append(names, filepath.ToSlash(rel))
	}

	more := ""
	if len(untracked) > shown {
		more = fmt.Sprintf(" and %d more", len(untracked)-shown)
	}

	req.Warn(fmt.Sprintf("%d files of %s belong to no rock and are left out of the archive: %s%s",
		len(untracked), req.Tree, strings.Join(names, ", "), more))
}

// Rocks-tree layout names, mirroring cli/manifest/build and go-luarocks.
const (
	// rocksInstallDir is where LuaRocks keeps per-rock metadata:
	// <tree>/share/tarantool/rocks/<name>/<version>/.
	rocksInstallDir = "share/tarantool/rocks"
	// treeLockFile is the lock of a tree, in its rocks dir.
	treeLockFile = ".lock"
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
	return copyTreeFiltered(src, dst, "", nil)
}

// copyTreeFiltered is copyTree with an optional skip predicate. relBase is the
// slash path src sits at inside the rocks tree, so the predicate always sees a
// tree-relative path however deep the copy root is - which is what lets the
// same filter apply to a whole-tree copy and to a per-namespace one. A nil skip
// copies everything.
func copyTreeFiltered(src, dst, relBase string, skip func(string) bool) error {
	realSrc, err := filepath.EvalSymlinks(src)
	if err == nil {
		src = realSrc
	}

	// Refuse to copy a tree into itself. The staging directory lives inside the
	// project (_build/pack/stage-*), so a payload entry naming an ancestor of it
	// would have WalkDir descend into the copies it is creating, recursing until
	// the path outgrows PATH_MAX. isReservedName rejects the entries that reach
	// this today; the guard keeps the invariant independent of that list.
	if within(dst, src) {
		return stateErrorf("refusing to copy %s into its own subdirectory %s", src, dst)
	}

	//nolint:wrapcheck // Every error the walk yields already names the entry it failed on.
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("copying %s: %w", path, err)
		}

		if skip != nil && skip(joinRel(relBase, rel)) {
			if entry.IsDir() {
				return fs.SkipDir
			}

			return nil
		}

		target := filepath.Join(dst, rel)

		if entry.IsDir() {
			return os.MkdirAll(target, dirPerm)
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("copying %s: %w", path, err)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			// Stat follows the link; a link to a directory becomes its own walk.
			resolved, err := os.Stat(path)
			if err != nil {
				// A dangling link is skipped rather than failing the pack.
				return nil //nolint:nilerr // A dangling symlink is not packable.
			}

			if resolved.IsDir() {
				return copyTreeFiltered(path, target, joinRel(relBase, rel), skip)
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

// joinRel appends a walk-relative path to the copy root's tree-relative base,
// in slash form. WalkDir reports the root itself as ".", which contributes
// nothing to the path.
func joinRel(base, rel string) string {
	slashed := filepath.ToSlash(rel)
	if slashed == "." {
		return base
	}

	if base == "" {
		return slashed
	}

	return base + "/" + slashed
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
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
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
	err := os.MkdirAll(filepath.Dir(dst), dirPerm)
	if err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}

	// Stat, not Lstat: a symlinked binary in bin_dir must be copied as its
	// target, since the archive carries no link structure.
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}

	perm := filePerm
	if info.Mode().Perm()&0o100 != 0 {
		perm = 0o755
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}

	defer func() { _ = srcFile.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}

	_, err = io.Copy(out, srcFile)
	if err != nil {
		_ = out.Close()

		return fmt.Errorf("copying %s: %w", src, err)
	}

	err = out.Close()
	if err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}

	return nil
}
