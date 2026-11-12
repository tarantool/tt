// Package rockindex writes the tt-rocks-index.json of a directory of rocks:
// the index that names every file of a rock version by URL, with its sha256
// and size, which tt package reads before any LuaRocks manifest (see
// remote.RocksIndex for the format and its rules).
//
// The index is made from the files of the directory, as the LuaRocks
// manifest is: <name>-<version>.<arch>.rock and <name>-<version>.rockspec
// directly in it, each listed at a URL that ends in its name. A directory
// that is a rock server has the index beside its manifest, the URLs relative
// to it; a directory whose files are served from elsewhere, release pages
// say, has them listed in an index kept apart, under the URL they are
// downloaded from.
package rockindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/tarantool/go-luarocks/remote"
)

// File is the name of the index in the directory it describes.
const File = remote.RocksIndexFile

const (
	// rockSuffix and rockspecSuffix end the names of the files indexed.
	rockSuffix     = ".rock"
	rockspecSuffix = ".rockspec"
	// dirPerm and filePerm are the modes of a created directory and of the
	// index, readable by a web server.
	dirPerm  fs.FileMode = 0o755
	filePerm fs.FileMode = 0o644
)

var (
	// ErrListedElsewhere reports a version of the directory that the index
	// lists with files elsewhere.
	ErrListedElsewhere = errors.New("listed with files elsewhere")
	// ErrNotBelowIndex reports a directory that an index with no base URL
	// cannot name the files of.
	ErrNotBelowIndex = errors.New("not below the directory of the index")

	errBaseQuery  = errors.New("a base URL has no query and no fragment")
	errNotRegular = errors.New("not a regular file")
)

// The names of the files indexed, as LuaRocks parses them (path.parse_name)
// and remote.MakeManifest lists them: <name>-<version>-<revision>.<arch>.rock
// and <name>-<version>-<revision>.rockspec.
var (
	rockNameRe     = regexp.MustCompile(`(?s)^(.*)-([^-]+-[0-9]+)\.([^.]+)\.rock$`)
	rockspecNameRe = regexp.MustCompile(`(?s)^(.*)-([^-]+-[0-9]+)\.rockspec$`)
)

// Options says which directory to index into which index.
type Options struct {
	// Dir is the directory of rocks. Required.
	Dir string
	// Index is the path of the index to write; empty is Dir/File.
	Index string
	// BaseURL is where the files of Dir are downloaded from: each is listed
	// as BaseURL/<its name>. It is an https URL or a path relative to the
	// directory of the index. Empty lists them relative to the index, by the
	// path from its directory to Dir, which must be below it.
	BaseURL string
}

// Result is what Write did.
type Result struct {
	// Index is the path of the index written.
	Index string
	// Listed counts the versions of Dir the index lists.
	Listed int
	// Kept counts the versions the index listed with files elsewhere, kept
	// as they were.
	Kept int
	// Dropped names the versions the index listed with files in Dir that
	// are gone from it, "<name> <version>" each, in order.
	Dropped []string
}

// Write (re)writes the index of opts.Dir. Every version whose files are in
// Dir is listed from them, its rockspec and the rock of each arch, at
// BaseURL/<file name>, with its sha256 and size.
//
// An index already at opts.Index is merged with: a version it lists with a
// file in Dir - at a URL of BaseURL/<a file name> - is listed anew from what
// Dir holds now, keeping "yanked": true, and left out when Dir holds none of
// it; a version it lists with files elsewhere is kept as it is. A version
// both in Dir and listed elsewhere fails the write, since a published
// version does not change: a new build is a new revision. So is a version of
// Dir an index cannot list, its name or its version not keeping the rules of
// remote.RocksIndex, and an index there that does not read.
//
// The index is written as remote.RocksIndex.Write writes it, the same index
// the same bytes, through a temporary file renamed over it.
func Write(ctx context.Context, opts Options) (Result, error) {
	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return Result{}, fmt.Errorf("rock index: %w", err)
	}

	indexPath := filepath.Join(dir, File)
	if opts.Index != "" {
		indexPath, err = filepath.Abs(opts.Index)
		if err != nil {
			return Result{}, fmt.Errorf("rock index: %w", err)
		}
	}

	prefix, err := urlPrefix(dir, filepath.Dir(indexPath), opts.BaseURL)
	if err != nil {
		return Result{}, err
	}

	listed, err := scan(ctx, dir, prefix)
	if err != nil {
		return Result{}, err
	}

	old, err := read(indexPath)
	if err != nil {
		return Result{}, err
	}

	out, result := merge(old, listed, prefix)

	result.Index = indexPath

	for _, name := range slices.Sorted(maps.Keys(listed)) {
		for _, version := range slices.Sorted(maps.Keys(listed[name])) {
			if kept, ok := out.Repository[name][version]; ok {
				return Result{}, fmt.Errorf("rock index %s: %s %s of %s is %w, at %s: a "+
					"published version does not change; pack the new build as a new "+
					"revision (--revision)", indexPath, name, version, dir,
					ErrListedElsewhere, firstURL(kept))
			}

			err = out.Add(name, version, listed[name][version])
			if errors.Is(err, remote.ErrDuplicateVersion) {
				return Result{}, fmt.Errorf("rock index %s: %s %s of %s: %w; a published "+
					"version does not change", indexPath, name, version, dir, err)
			}

			if err != nil {
				return Result{}, fmt.Errorf("rock index %s: %s %s of %s: %w",
					indexPath, name, version, dir, err)
			}

			result.Listed++
		}
	}

	err = writeAtomic(indexPath, out)
	if err != nil {
		return Result{}, err
	}

	return result, nil
}

// urlPrefix is what the URL of a file of dir is, but for its name: base and
// a slash, or the path from indexDir to dir and a slash, or nothing when the
// index is in dir.
func urlPrefix(dir, indexDir, base string) (string, error) {
	if base != "" {
		parsed, err := url.Parse(base)
		if err != nil {
			return "", fmt.Errorf("rock index: base URL %q: %w", base, err)
		}

		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", fmt.Errorf("rock index: base URL %q: %w", base, errBaseQuery)
		}

		return strings.TrimRight(base, "/") + "/", nil
	}

	rel, err := filepath.Rel(indexDir, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("rock index: %s is %w, %s, so the index cannot name its files "+
			"by a relative URL: give the URL they are downloaded from (--base-url)",
			dir, ErrNotBelowIndex, indexDir)
	}

	if rel == "." {
		return "", nil
	}

	return filepath.ToSlash(rel) + "/", nil
}

// listing is what scan finds in a directory: name to version to entry.
type listing = map[string]map[string]remote.IndexVersion

// rockFile is a file of a rock server: the rock name, version and arch it is
// of, the arch empty for a rockspec.
type rockFile struct {
	name, version, arch string
}

// scan lists the versions whose files are in dir, each file at prefix and its
// name.
func scan(ctx context.Context, dir, prefix string) (listing, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("rock index: %w", err)
	}

	defer func() { _ = root.Close() }()

	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("rock index: list %s: %w", dir, err)
	}

	listed := listing{}

	for _, entry := range entries {
		err = ctx.Err()
		if err != nil {
			return nil, fmt.Errorf("rock index: %w", err)
		}

		// The names the manifest leaves out are left out here too: those
		// starting with a dot, and those with a newline.
		file := entry.Name()
		if strings.HasPrefix(file, ".") || strings.Contains(file, "\n") {
			continue
		}

		parsed, ok := parseFileName(file)
		if !ok {
			continue
		}

		sum, size, digestErr := digest(root, file)
		if digestErr != nil {
			return nil, fmt.Errorf("rock index: %s: %w", filepath.Join(dir, file), digestErr)
		}

		if listed[parsed.name] == nil {
			listed[parsed.name] = map[string]remote.IndexVersion{}
		}

		files := listed[parsed.name][parsed.version]
		if parsed.arch == "" {
			files.Rockspec = &remote.IndexFile{SHA256: sum, Size: size, URL: prefix + file}
		} else {
			files.Artifacts = append(files.Artifacts, remote.IndexArtifact{
				Arch: parsed.arch, SHA256: sum, Size: size, URL: prefix + file,
			})
		}

		listed[parsed.name][parsed.version] = files
	}

	return listed, nil
}

// parseFileName tells what rock file is; ok is false for any other file.
func parseFileName(file string) (rockFile, bool) {
	if strings.HasSuffix(file, rockSuffix) {
		if m := rockNameRe.FindStringSubmatch(file); m != nil {
			return rockFile{name: m[1], version: m[2], arch: m[3]}, true
		}
	}

	if strings.HasSuffix(file, rockspecSuffix) {
		if m := rockspecNameRe.FindStringSubmatch(file); m != nil {
			return rockFile{name: m[1], version: m[2], arch: ""}, true
		}
	}

	return rockFile{name: "", version: "", arch: ""}, false
}

// digest returns the sha256 in hex and the size of the regular file of root.
func digest(root *os.Root, file string) (string, int64, error) {
	opened, err := root.Open(file)
	if err != nil {
		return "", 0, fmt.Errorf("open: %w", err)
	}

	defer func() { _ = opened.Close() }()

	info, err := opened.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("stat: %w", err)
	}

	if !info.Mode().IsRegular() {
		return "", 0, errNotRegular
	}

	hash := sha256.New()

	size, err := io.Copy(hash, opened)
	if err != nil {
		return "", 0, fmt.Errorf("read: %w", err)
	}

	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

// read reads the index at path, or returns nil when there is none.
func read(path string) (*remote.RocksIndex, error) {
	opened, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil //nolint:nilnil // No index yet: there is nothing to merge with.
	}

	if err != nil {
		return nil, fmt.Errorf("rock index: %w", err)
	}

	defer func() { _ = opened.Close() }()

	idx, err := remote.ReadRocksIndex(opened)
	if err != nil {
		return nil, fmt.Errorf("rock index %s does not read, so it is not rewritten: %w", path, err)
	}

	return idx, nil
}

// merge starts the new index from old: the versions old lists with files
// elsewhere than at prefix are kept, and those of the directory are left to
// be listed from what it holds, listed taking over their yanked flag, or
// counted dropped when it holds none of them.
func merge(old *remote.RocksIndex, listed listing, prefix string) (*remote.RocksIndex, Result) {
	out := remote.NewRocksIndex()
	result := Result{Index: "", Listed: 0, Kept: 0, Dropped: nil}

	if old == nil {
		return out, result
	}

	for _, name := range slices.Sorted(maps.Keys(old.Repository)) {
		versions := old.Repository[name]

		for _, version := range slices.Sorted(maps.Keys(versions)) {
			entry := versions[version]

			if !ofDir(entry, prefix) {
				if out.Repository[name] == nil {
					out.Repository[name] = map[string]remote.IndexVersion{}
				}

				out.Repository[name][version] = entry
				result.Kept++

				continue
			}

			now, ok := listed[name][version]
			if !ok {
				result.Dropped = append(result.Dropped, name+" "+version)

				continue
			}

			now.Yanked = entry.Yanked
			listed[name][version] = now
		}
	}

	return out, result
}

// ofDir reports whether entry lists a file of the directory whose files are
// at prefix: a URL of prefix and a file name.
func ofDir(entry remote.IndexVersion, prefix string) bool {
	for _, u := range urls(entry) {
		rest, ok := strings.CutPrefix(u, prefix)
		if ok && rest != "" && !strings.Contains(rest, "/") {
			return true
		}
	}

	return false
}

// urls lists the URLs of the files of entry, the rockspec first.
func urls(entry remote.IndexVersion) []string {
	out := make([]string, 0, len(entry.Artifacts)+1)
	if entry.Rockspec != nil {
		out = append(out, entry.Rockspec.URL)
	}

	for _, artifact := range entry.Artifacts {
		out = append(out, artifact.URL)
	}

	return out
}

// firstURL is the URL of a file of entry, for an error to point at it.
func firstURL(entry remote.IndexVersion) string {
	if all := urls(entry); len(all) > 0 {
		return all[0]
	}

	return "no url"
}

// writeAtomic writes idx to path through a temporary file in its directory
// and a rename, so a reader sees the old index or the new one.
func writeAtomic(path string, idx *remote.RocksIndex) error {
	dir := filepath.Dir(path)

	err := os.MkdirAll(dir, dirPerm)
	if err != nil {
		return fmt.Errorf("rock index: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("rock index: %w", err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	err = idx.Write(tmp)
	if err == nil {
		err = tmp.Chmod(filePerm)
	}

	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		return fmt.Errorf("rock index: writing %s: %w", path, err)
	}

	return nil
}
