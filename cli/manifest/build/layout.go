package build

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/cli/manifest"
)

// Rocks-tree layout roots. Component .lua files land under the share subtree and
// compiled .so modules under the lib subtree, mirroring where Tarantool's
// require and box.schema.func look. The namespace segment is appended to these
// (empty namespace => flat, no segment).
const (
	shareTarantool = "share/tarantool"
	libTarantool   = "lib/tarantool"
)

// sharedObjectExt selects the lib subtree; every other kept file goes to share.
const sharedObjectExt = ".so"

// filePerm / dirPerm are the modes for laid-out files and their parents. Source
// permissions are preserved when readable; these are the fallbacks.
const (
	filePerm os.FileMode = 0o644
	dirPerm  os.FileMode = 0o750
)

// layoutComponent copies one component's files into the rocks tree under its
// namespace. Files are taken from the component's path, filtered by
// include/exclude (see fileFilter), and split by extension: .so into
// <tree>/lib/tarantool/<namespace>/, everything else into
// <tree>/share/tarantool/<namespace>/, each keeping its path relative to the
// component root. An empty namespace yields a flat layout with no namespace
// segment.
//
// It returns the absolute destination paths it wrote, in deterministic walk
// order, so the caller can detect a component that laid down a file colliding
// with the generated version.lua.
func layoutComponent(
	tree, pkgName string, component manifest.Component, compAbsPath string,
) ([]string, error) {
	filter := newFileFilter(component)
	namespace := component.EffectiveNamespace(pkgName)

	var written []string

	walkErr := filepath.WalkDir(compAbsPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := relComponents(compAbsPath, path)
		if relErr != nil {
			return relErr
		}

		// The component root itself is never a candidate and never pruned.
		if len(rel) == 0 {
			return nil
		}

		if entry.IsDir() {
			if filter.pruneDir(rel) {
				return fs.SkipDir
			}

			return nil
		}

		if !entry.Type().IsRegular() || !filter.keepFile(rel) {
			return nil
		}

		dst := destPath(tree, namespace, rel)

		copyErr := copyFile(path, dst)
		if copyErr != nil {
			return fmt.Errorf("laying out %s: %w", filepath.Join(rel...), copyErr)
		}

		written = append(written, dst)

		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("component %q: %w", component.Path, walkErr)
	}

	return written, nil
}

// relComponents returns path split into slash-separated components relative to
// root. The component root itself yields an empty slice.
func relComponents(root, path string) ([]string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, fmt.Errorf("relativizing %q: %w", path, err)
	}

	if rel == "." {
		return nil, nil
	}

	return strings.Split(filepath.ToSlash(rel), "/"), nil
}

// destPath builds the absolute destination for a kept file: the share or lib
// subtree (by .so extension), the namespace segment when non-empty, then the
// file's path relative to the component root.
func destPath(tree, namespace string, rel []string) string {
	sub := shareTarantool
	if strings.HasSuffix(rel[len(rel)-1], sharedObjectExt) {
		sub = libTarantool
	}

	parts := []string{tree, sub}
	if namespace != "" {
		parts = append(parts, namespace)
	}

	parts = append(parts, rel...)

	return filepath.Join(parts...)
}

// copyFile copies src to dst, creating dst's parent directory and preserving
// the source's permission bits (falling back to filePerm when unreadable).
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src) //nolint:gosec // Copies the caller's own component files.
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}

	defer func() { _ = srcFile.Close() }()

	mode := filePerm

	info, statErr := srcFile.Stat()
	if statErr == nil && info.Mode().Perm() != 0 {
		mode = info.Mode().Perm()
	}

	mkErr := os.MkdirAll(filepath.Dir(dst), dirPerm)
	if mkErr != nil {
		return fmt.Errorf("create destination dir: %w", mkErr)
	}

	//nolint:gosec // dst is derived from the manifest's own component tree.
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}

	_, copyErr := io.Copy(dstFile, srcFile)
	if copyErr != nil {
		_ = dstFile.Close()

		return fmt.Errorf("copy contents: %w", copyErr)
	}

	closeErr := dstFile.Close()
	if closeErr != nil {
		return fmt.Errorf("close destination: %w", closeErr)
	}

	return nil
}
