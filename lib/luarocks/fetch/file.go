package fetch

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// dirPerm is the mode for directories created while copying a tree:
	// owner rwx, group rx, no world access.
	dirPerm = 0o750

	// ownerSearchBit forces the owner-search (execute) bit on directories
	// so we can descend into them even when the source mode omits it.
	ownerSearchBit = 0o100
)

// fileBackend implements file:// fetches by copying the source tree into
// destDir. We always copy (rather than returning the on-disk path
// directly) so that build steps which scribble into the working tree
// don't corrupt the user's local source — matches upstream luarocks's
// `fetch.fetch_local`.
type fileBackend struct{}

// Fetch strips the `file://` prefix, validates the source path, copies
// the tree into destDir, and returns the destination path.
func (fileBackend) Fetch(ctx context.Context, rawURL, destDir string, _ Options) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	src := strings.TrimPrefix(rawURL, "file://")

	st, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("fetch.file: stat %q: %w", src, err)
	}

	if err := os.MkdirAll(destDir, dirPerm); err != nil {
		return "", fmt.Errorf("fetch.file: mkdir %q: %w", destDir, err)
	}

	if !st.IsDir() {
		// Single file (e.g. a rockspec or archive). Copy into destDir
		// under its basename and return destDir.
		dst := filepath.Join(destDir, filepath.Base(src))

		err := copyOneFile(src, dst, st.Mode().Perm())
		if err != nil {
			return "", err
		}

		return destDir, nil
	}
	// Copy tree rooted at src into destDir.
	if err := copyDir(ctx, src, destDir); err != nil {
		return "", fmt.Errorf("fetch.file: %w", err)
	}

	return destDir, nil
}

func copyDir(ctx context.Context, src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()|ownerSearchBit)
		}

		return copyOneFile(p, target, info.Mode().Perm())
	})
}

func copyOneFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return fmt.Errorf("mkdir %q: %w", filepath.Dir(dst), err)
	}

	// src is a caller-supplied file:// path being copied into destDir; reading
	// the user's own local source tree is the documented purpose of this backend.
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %q: %w", src, err)
	}

	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create %q: %w", dst, err)
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()

		return fmt.Errorf("copy %q->%q: %w", src, dst, err)
	}

	return out.Close()
}
