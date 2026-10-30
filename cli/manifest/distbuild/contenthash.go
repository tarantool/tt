package distbuild

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/manifest"
)

// contentHashPrefix tags a content hash with its algorithm, as manifest_hash
// is tagged.
const contentHashPrefix = "sha256:"

// errLocalChanged reports a local core or module whose files differ from
// what the lock recorded.
var errLocalChanged = errors.New("changed since " + manifest.DistributionLockFile +
	" was written")

// hashRoot is a directory whose files a content hash covers, under a prefix.
type hashRoot struct {
	// dir is the directory, absolute.
	dir string
	// prefix is the slash-separated path its files are listed under; empty
	// lists them by their paths in dir.
	prefix string
}

// contentHash returns the content hash of the files of the module
// directories roots: every regular file the bundle of the distribution
// would copy for them (see skipModuleDir; symbolic links and other
// non-regular files are left out, as the bundle leaves them), listed by its
// slash-separated path under the root's prefix. The hash is the SHA-256 of
// the listing in the order of the paths, one line per file - the hex
// SHA-256 of its content, two spaces and its path - the shape of the h1:
// hash Go puts in go.sum, so the same files hash the same wherever they
// are and whatever their modes and times. work is the directory of the
// build, which is no module's.
func contentHash(roots []hashRoot, work string) (string, error) {
	files := map[string]string{}

	for _, root := range roots {
		err := filepath.WalkDir(root.dir, func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if entry.IsDir() {
				if skipModuleDir(root.dir, name, entry, work) {
					return filepath.SkipDir
				}

				return nil
			}

			if !entry.Type().IsRegular() {
				return nil
			}

			rel, err := filepath.Rel(root.dir, name)
			if err != nil {
				return fmt.Errorf("listing %s: %w", name, err)
			}

			files[path.Join(root.prefix, filepath.ToSlash(rel))] = name

			return nil
		})
		if err != nil {
			return "", sdk.Errorf(sdk.ExitSystem, "hashing %s: %w", root.dir, err)
		}
	}

	listing := sha256.New()

	for _, rel := range slices.Sorted(maps.Keys(files)) {
		sum, err := fileHash(files[rel])
		if err != nil {
			return "", sdk.Errorf(sdk.ExitSystem, "hashing %s: %w", files[rel], err)
		}

		_, _ = fmt.Fprintf(listing, "%s  %s\n", sum, rel)
	}

	return contentHashPrefix + hex.EncodeToString(listing.Sum(nil)), nil
}

// fileHash returns the hex SHA-256 of the content of the file name.
func fileHash(name string) (string, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", fmt.Errorf("opening: %w", err)
	}

	defer func() { _ = file.Close() }()

	sum := sha256.New()

	_, err = io.Copy(sum, file)
	if err != nil {
		return "", fmt.Errorf("reading: %w", err)
	}

	return hex.EncodeToString(sum.Sum(nil)), nil
}

// contentHashes are the content hashes of the local modules of a build.
type contentHashes struct {
	// core is the hash of a local core with the modules of its repository;
	// empty for a versioned core.
	core string
	// modules are the hashes of the local tt modules, by name.
	modules map[string]string
	// replaces are the hashes of the directories [replace] names, by key.
	replaces map[string]string
}

// hashLocal returns the content hashes of the local core, the local tt
// modules and the directories [replace] names. A local core is hashed with
// every module of its repository its go.mod replaces with a directory: they
// are built from those directories as much as the core is. A module of the
// repository outside the core's directory is listed under
// external/<module path>.
func (b builder) hashLocal(
	resolved core, modules []module, replaces []replacement,
) (contentHashes, error) {
	hashes := contentHashes{core: "", modules: map[string]string{}, replaces: map[string]string{}}

	if resolved.Dir != "" {
		roots := []hashRoot{{dir: resolved.Dir, prefix: ""}}

		for _, repo := range resolved.Repository {
			if repo.Dir == "" {
				continue
			}

			prefix := path.Join("external", repo.Module)

			rel, err := filepath.Rel(resolved.Dir, repo.Dir)
			if err == nil && filepath.IsLocal(rel) {
				prefix = filepath.ToSlash(rel)
			}

			roots = append(roots, hashRoot{dir: repo.Dir, prefix: prefix})
		}

		sum, err := contentHash(roots, b.work)
		if err != nil {
			return contentHashes{}, err
		}

		hashes.core = sum
	}

	for _, mod := range modules {
		if mod.Dir == "" {
			continue
		}

		sum, err := contentHash([]hashRoot{{dir: mod.Dir, prefix: ""}}, b.work)
		if err != nil {
			return contentHashes{}, err
		}

		hashes.modules[mod.Name] = sum
	}

	for _, rep := range replaces {
		if rep.Dir == "" {
			continue
		}

		sum, err := contentHash([]hashRoot{{dir: rep.Dir, prefix: ""}}, b.work)
		if err != nil {
			return contentHashes{}, err
		}

		hashes.replaces[rep.Key] = sum
	}

	return hashes, nil
}

// checkContentHashes checks, for a build from the lock, that the local core,
// the local modules and the directories [replace] names hold what they held
// when the lock was written.
func (b builder) checkContentHashes(
	resolved core, modules []module, replaces []replacement, hashes contentHashes,
) error {
	var changed []string

	if resolved.Dir != "" && hashes.core != b.lock.Core.ContentHash {
		changed = append(changed, resolved.Path)
	}

	for _, mod := range modules {
		if mod.Dir == "" {
			continue
		}

		if hashes.modules[mod.Name] != b.lock.Modules[mod.Name].ContentHash {
			changed = append(changed, mod.Path)
		}
	}

	for _, rep := range replaces {
		if rep.Dir == "" {
			continue
		}

		if hashes.replaces[rep.Key] != b.lock.Replace[rep.Key].ContentHash {
			changed = append(changed, rep.Path+" (replace "+rep.Key+")")
		}
	}

	if len(changed) == 0 {
		return nil
	}

	return sdk.Errorf(sdk.ExitFailure, "%s %w; build without --locked to update it",
		strings.Join(changed, ", "), errLocalChanged)
}
