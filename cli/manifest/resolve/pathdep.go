package resolve

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/rocks"
	luarocks "github.com/tarantool/tt/lib/luarocks"
	"github.com/tarantool/tt/lib/luarocks/deps"
)

// resolvePath resolves a path dependency: it hashes the local directory and,
// if the directory ships a rockspec, reads the version and transitive
// dependencies from it. A path dependency is pinned by path and content hash
// rather than by registry version and checksum.
func (e *Engine) resolvePath(req depReq) (*resolvedDep, []depReq, error) {
	dir := filepath.Join(e.projectDir, req.path)

	hash, hashErr := contentHash(dir)
	if hashErr != nil {
		return nil, nil, fmt.Errorf("hashing path dependency %q: %w", req.name, hashErr)
	}

	spec, metaErr := e.adapter.LocalMetadata(dir)
	if metaErr != nil && !errors.Is(metaErr, rocks.ErrNoLocalRockspec) {
		return nil, nil, fmt.Errorf("local metadata for %q: %w", req.name, metaErr)
	}

	var (
		version  luarocks.Version
		children []depReq
	)

	if spec != nil {
		// Best-effort: a malformed version leaves the pin's parsed form zero,
		// which only matters if another branch constrains this same name.
		version, _ = deps.ParseVersion(spec.Version)
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
// length before the content so distinct trees cannot collide. Non-regular
// entries (symlinks, devices) are skipped rather than followed, so a symlink to
// a directory does not abort the hash. Directory structure without files does
// not contribute.
func contentHash(dir string) (string, error) {
	files, walkErr := walkFiles(dir)
	if walkErr != nil {
		return "", walkErr
	}

	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	hasher := sha256.New()

	for _, file := range files {
		//nolint:gosec // engine-owned project path, not user-controlled input
		content, readErr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file.rel)))
		if readErr != nil {
			return "", fmt.Errorf("reading %s: %w", file.rel, readErr)
		}

		_, _ = fmt.Fprintf(hasher, "%s\x00%d\x00%d\x00",
			file.rel, boolToInt(file.exec), len(content))
		_, _ = hasher.Write(content)
	}

	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
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
