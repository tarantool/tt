package pack

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/go-luarocks/manif"

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
	// DevOnly lists the locked rocks that only the dev closure brings. Their
	// files are dropped from .rocks/ in both packing modes: they are the
	// developer's tools, and the spec is explicit that they never reach the
	// archive. See devOnlyRocks.
	DevOnly []manifest.LockDependency
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
//
// Dev-only rocks are dropped in both modes. The two filters compose rather than
// override: --without-deps decides which subtrees are copied at all, the dev
// filter decides which files inside a copied subtree are skipped, and the skip
// predicate is evaluated against the tree-relative path either way, so a
// namespace that happens to share a dev rock's name is not a hole in it.
func stageRocks(stageDir string, req stageRequest) error {
	if _, err := os.Stat(req.Tree); os.IsNotExist(err) {
		// A pure-metadata package need not have produced a tree.
		return nil
	}

	dstRocks := filepath.Join(stageDir, rocksDirName)
	skip := devRockFilter(req.Tree, req.DevOnly)

	if req.WithDeps {
		return copyTreeFiltered(req.Tree, dstRocks, "", skip)
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

			copyErr := copyTreeFiltered(src, dst, sub+"/"+ns, skip)
			if copyErr != nil {
				return copyErr
			}
		}
	}

	return nil
}

// Rocks-tree layout roots, mirroring cli/manifest/build.
const (
	shareTarantool = "share/tarantool"
	libTarantool   = "lib/tarantool"
	// rocksInstallDir is where LuaRocks keeps per-rock metadata:
	// <tree>/share/tarantool/rocks/<name>/<version>/.
	rocksInstallDir = shareTarantool + "/rocks"
	// binDir holds the console scripts a rock installs. luatest - the canonical
	// dev dependency - ships one, which is why the filter has to reach here and
	// not only into the two module trees.
	binDir = "bin"
	// rockManifestFile enumerates, per rock, every file it deployed.
	rockManifestFile = "rock_manifest"
)

// devRockFilter builds the predicate stageRocks skips paths by: it reports
// whether a tree-relative slash path belongs to one of the dev-only rocks. A
// nil DevOnly yields nil, which copyTreeFiltered reads as "copy everything".
//
// A rock's footprint is taken from its own rock_manifest, which lists every
// file it deployed - the authoritative answer, and the only one that catches a
// rock installing a bare share/tarantool/<name>.lua rather than a directory, or
// a console script in bin/. Its per-rock metadata directory is excluded whole
// and separately, since that is where the rock_manifest itself, the rockspec,
// doc/ and conf/ live.
//
// A rock whose rock_manifest is missing or unreadable falls back to the
// name-keyed directories cli/manifest/state.RockPaths uses. That under-excludes
// a rock laying files outside them, which is the safe direction to be wrong in:
// the archive keeps a file it did not need rather than losing one it did.
func devRockFilter(tree string, devOnly []manifest.LockDependency) func(string) bool {
	if len(devOnly) == 0 {
		return nil
	}

	excluded := map[string]bool{}

	for _, rock := range devOnly {
		excluded[rocksInstallDir+"/"+rock.Name] = true

		deployed, ok := deployedPaths(tree, rock)
		if !ok {
			excluded[shareTarantool+"/"+rock.Name] = true
			excluded[libTarantool+"/"+rock.Name] = true

			continue
		}

		for _, path := range deployed {
			excluded[path] = true
		}
	}

	return func(rel string) bool {
		for path := range excluded {
			if rel == path || strings.HasPrefix(rel, path+"/") {
				return true
			}
		}

		return false
	}
}

// deployedPaths reads one rock's rock_manifest and returns the tree-relative
// paths it deployed outside its own metadata directory. The second result is
// false when the rock_manifest cannot be read, which is the caller's signal to
// fall back to the name-keyed directories.
func deployedPaths(tree string, rock manifest.LockDependency) ([]string, bool) {
	specPath := filepath.Join(tree, filepath.FromSlash(rocksInstallDir),
		rock.Name, rock.Version, rockManifestFile)

	rockManifest, err := manif.FileStore{}.ReadRock(specPath)
	if err != nil || rockManifest == nil {
		return nil, false
	}

	var paths []string

	for root, files := range map[string]map[string]string{
		shareTarantool: rockManifest.Lua,
		libTarantool:   rockManifest.Lib,
		binDir:         rockManifest.Bin,
	} {
		for file := range files {
			paths = append(paths, root+"/"+filepath.ToSlash(file))
		}
	}

	return paths, true
}

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

		if skip != nil && skip(joinRel(relBase, rel)) {
			if d.IsDir() {
				return fs.SkipDir
			}

			return nil
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
