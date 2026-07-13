package resolve

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/deps"
	"github.com/tarantool/tt/cli/manifest"
)

// resolvePath resolves a path dependency: it hashes the local directory and,
// if the directory ships a rockspec, reads the version and transitive
// dependencies from it. A path dependency is pinned by path and content hash
// rather than by registry version and checksum.
func (w *walker) resolvePath(req depReq) (*resolvedDep, []depReq, error) {
	dir := filepath.Join(w.engine.projectDir, req.path)

	hash, hashErr := w.cache.dirContentHash(dir)
	if hashErr != nil {
		return nil, nil, fmt.Errorf("hashing path dependency %q: %w", req.name, hashErr)
	}

	spec, metaErr := w.cache.localMetadata(w.engine.adapter, dir)
	if metaErr != nil {
		return nil, nil, fmt.Errorf("local metadata for %q: %w", req.name, metaErr)
	}

	var (
		version  luarocks.Version
		children []depReq
	)

	if spec != nil {
		parsed, parseErr := deps.ParseVersion(spec.Version)
		if parseErr != nil {
			// Non-fatal: the pin still holds by content hash, and a path
			// dependency satisfies any constraint by fiat (see walker.walk), so a
			// zero version cannot force a false conflict. Surface it as a warning
			// rather than discarding it silently.
			w.warnings = append(w.warnings, fmt.Sprintf(
				"path dependency %q has an unparseable version %q; pinned by content hash only",
				req.name, spec.Version))
		} else {
			version = parsed
		}

		children = transitiveReqs(spec.Dependencies)
	}

	resolved := &resolvedDep{
		lockDep: manifest.LockDependency{
			Name:        req.name,
			Version:     version.Raw,
			Source:      manifestSourcePath,
			Checksum:    "",
			Path:        req.path,
			ContentHash: hash,
		},
		version: version,
	}

	return resolved, children, nil
}

// fileEntry is one regular file discovered under a path dependency: its
// slash-normalized path relative to the root and whether it is executable.
type fileEntry struct {
	rel  string
	exec bool
}

// contentHash is the SHA-256 of a directory's contents, tagged "sha256:". It
// walks every regular file, sorts them by slash-normalized relative path for a
// platform-stable order, and frames each path, its executable bit and its byte
// length before the content so distinct trees cannot collide. Symlinks
// discovered *inside* the tree are skipped rather than followed, so a symlink to
// a directory does not abort the hash; a symlinked *root* is resolved first so a
// module vendored as a symlink still hashes its contents. Directory structure
// without files does not contribute.
func contentHash(dir string) (string, error) {
	// Resolve a symlinked root so its contents are walked (WalkDir does not
	// descend a symlink). Symlinks *within* the tree are still skipped below.
	root, symErr := filepath.EvalSymlinks(dir)
	if symErr != nil {
		return "", fmt.Errorf("resolving %s: %w", dir, symErr)
	}

	files, walkErr := walkFiles(root)
	if walkErr != nil {
		return "", walkErr
	}

	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	hasher := sha256.New()

	for _, file := range files {
		hashErr := hashFile(hasher, root, file)
		if hashErr != nil {
			return "", hashErr
		}
	}

	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

// hashFile frames one file's relative path, executable bit and byte length into
// hasher, then streams its content, so a large file is not read whole into
// memory. The size prefix is the delimiter that keeps distinct trees from
// colliding; it is read from the open handle so it matches the bytes streamed.
func hashFile(hasher io.Writer, root string, file fileEntry) error {
	//nolint:gosec // engine-owned project path, not user-controlled input
	handle, openErr := os.Open(filepath.Join(root, filepath.FromSlash(file.rel)))
	if openErr != nil {
		return fmt.Errorf("opening %s: %w", file.rel, openErr)
	}

	defer func() { _ = handle.Close() }()

	info, statErr := handle.Stat()
	if statErr != nil {
		return fmt.Errorf("stat %s: %w", file.rel, statErr)
	}

	_, _ = fmt.Fprintf(hasher, "%s\x00%d\x00%d\x00",
		file.rel, boolToInt(file.exec), info.Size())

	_, copyErr := io.Copy(hasher, handle)
	if copyErr != nil {
		return fmt.Errorf("reading %s: %w", file.rel, copyErr)
	}

	return nil
}

// walkFiles collects every regular file under dir as a fileEntry.
func walkFiles(dir string) ([]fileEntry, error) {
	var files []fileEntry

	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !entry.Type().IsRegular() {
			// Directories, symlinks, devices, sockets: not content to hash.
			return nil
		}

		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return fmt.Errorf("relativizing %s: %w", path, relErr)
		}

		info, infoErr := entry.Info()
		if infoErr != nil {
			return fmt.Errorf("stat %s: %w", path, infoErr)
		}

		files = append(files, fileEntry{
			rel:  filepath.ToSlash(rel),
			exec: info.Mode().Perm()&0o111 != 0,
		})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", dir, err)
	}

	return files, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}

	return 0
}
