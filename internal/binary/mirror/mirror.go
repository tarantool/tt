// Package mirror keeps the programs a project runs on in a mirror: the
// binaries/ directory of a directory tt package download makes a rock server
// of, with the archives of tarantool, tt and TCM in the formats sdk/bundle
// defines and an index of them, tt-binaries-index.json.
//
// tt package download puts the programs the lock of a project pins into it
// (Add), repacked from the installations of the project or fetched from the
// sources of prebuilt archives; tt binary install takes them from it on a
// machine with no network, a mirror among the rock servers of the command
// being a source of prebuilt archives (Source) that is asked before the
// others.
//
// The layout:
//
//	<mirror>/binaries/tt-binaries-index.json
//	<mirror>/binaries/tarantool-3.9.0-linux-amd64.tar.gz
//	<mirror>/binaries/tt-3.0.0-linux-amd64.tar.gz
//
// The index lists every archive with what it is the build of and its
// digest:
//
//	{
//	  "format": 1,
//	  "binaries": [
//	    {
//	      "product": "tarantool",
//	      "version": "3.9.0",
//	      "os": "linux",
//	      "arch": "amd64",
//	      "file": "tarantool-3.9.0-linux-amd64.tar.gz",
//	      "sha256": "<64 hex digits>",
//	      "size": 41234567
//	    }
//	  ]
//	}
//
// product is the product as tt binary names it, so the edition is in it:
// tarantool, tarantool-ee, tt, tt-ee, tcm. version and flavor are those of
// the build, as a source lists them (bundle.Artifact): an archive of the
// customer zone keeps the flavours of its name, "flavor": ["gc64"]. os and
// arch are runtime.GOOS and runtime.GOARCH of the platform it runs on.
package mirror

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// DirName is the directory of a mirror the programs are kept in. No
	// build backend is named so: the backends vendor into <mirror>/<backend>.
	DirName = "binaries"
	// IndexFile is the index of the archives, in DirName.
	IndexFile = "tt-binaries-index.json"
	// IndexFormat is the format of the index this tt writes and reads.
	IndexFormat = 1

	dirMode  = 0o755
	fileMode = 0o644
)

var (
	// ErrIndex reports an index tt cannot read.
	ErrIndex = errors.New("invalid index of binaries")
	// errChecksum reports an archive whose digest is not the one the index
	// lists.
	errChecksum = errors.New("archive checksum mismatch")
)

// Entry is one archive of the index.
type Entry struct {
	// Product is the product as tt binary names it: "tarantool", "tt-ee".
	Product string `json:"product"`
	// Version is the version of the build, without its flavours.
	Version string `json:"version"`
	// Flavor lists the flavours of the build: "gc64" of an SDK archive.
	Flavor []string `json:"flavor,omitempty"`
	// OS and Arch are the platform the build runs on.
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// File is the name of the archive in DirName.
	File string `json:"file"`
	// SHA256 is the hex SHA-256 digest of the archive.
	SHA256 string `json:"sha256"`
	// Size is the size of the archive in bytes.
	Size int64 `json:"size"`
}

// sameBuild reports whether the entries are of the same build: a second
// download of it replaces the first.
func (e Entry) sameBuild(other Entry) bool {
	return e.Product == other.Product && e.Version == other.Version &&
		slices.Equal(e.Flavor, other.Flavor) && e.OS == other.OS && e.Arch == other.Arch
}

// Index is the index of the archives of a mirror.
type Index struct {
	// Format is IndexFormat.
	Format int `json:"format"`
	// Binaries are the archives, sorted by product, platform and version.
	Binaries []Entry `json:"binaries"`
}

// IndexPath is the index of the mirror in dir.
func IndexPath(dir string) string {
	return filepath.Join(dir, DirName, IndexFile)
}

// ReadIndex reads the index of the mirror in dir. found is false when the
// mirror has no index: a rock server with no programs, or no mirror at all.
func ReadIndex(dir string) (Index, bool, error) {
	path := IndexPath(dir)

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrInvalid) {
		return Index{Format: IndexFormat, Binaries: nil}, false, nil
	}

	if err != nil {
		return Index{}, false, fmt.Errorf("reading %s: %w", path, err)
	}

	var index Index

	err = json.Unmarshal(data, &index)
	if err != nil {
		return Index{}, false, fmt.Errorf("%w %s: %w", ErrIndex, path, err)
	}

	if index.Format != IndexFormat {
		return Index{}, false, fmt.Errorf("%w %s: format %d, this tt reads %d", ErrIndex,
			path, index.Format, IndexFormat)
	}

	for _, entry := range index.Binaries {
		if !filepath.IsLocal(entry.File) || strings.ContainsAny(entry.File, `/\`) {
			return Index{}, false, fmt.Errorf("%w %s: %q is not a file of %s", ErrIndex,
				path, entry.File, filepath.Dir(path))
		}
	}

	return index, true, nil
}

// put adds entry to the index, replacing the entry of the same build, and
// keeps the entries sorted.
func (index *Index) put(entry Entry) {
	index.Binaries = slices.DeleteFunc(index.Binaries, entry.sameBuild)
	index.Binaries = append(index.Binaries, entry)

	slices.SortFunc(index.Binaries, func(left, right Entry) int {
		for _, pair := range [][2]string{
			{left.Product, right.Product}, {left.OS, right.OS}, {left.Arch, right.Arch},
			{left.Version, right.Version},
			{strings.Join(left.Flavor, "-"), strings.Join(right.Flavor, "-")},
		} {
			if order := strings.Compare(pair[0], pair[1]); order != 0 {
				return order
			}
		}

		return 0
	})
}

// writeIndex writes the index of the mirror in dir, whole or not at all.
func writeIndex(dir string, index Index) error {
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the index of binaries: %w", err)
	}

	path := IndexPath(dir)

	temp, err := os.CreateTemp(filepath.Dir(path), "."+IndexFile+"-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	defer func() { _ = os.Remove(temp.Name()) }()

	_, err = temp.Write(append(data, '\n'))

	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Chmod(temp.Name(), fileMode)
	}

	if err == nil {
		err = os.Rename(temp.Name(), path)
	}

	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}
