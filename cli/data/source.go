package data

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/tarantool/go-xlog/format"
	"github.com/tarantool/go-xlog/reader"

	"github.com/tarantool/tt/sdk/log"
)

// Kind is the kind of a journal file.
type Kind int

const (
	// KindSnap is a snapshot: the state of the database at its vclock.
	KindSnap Kind = iota
	// KindXlog is a write-ahead log: the transactions after its vclock.
	KindXlog
	// KindVylog is a vinyl metadata log: the runs and ranges of the vinyl
	// spaces.
	KindVylog
)

// Extensions of the journal files, which name their kinds.
const (
	extSnap  = ".snap"
	extXlog  = ".xlog"
	extVylog = ".vylog"
)

var (
	// ErrNotJournal is a file named on the command line that is not a
	// journal file.
	ErrNotJournal = errors.New("not a journal file: want a .snap, .xlog or .vylog file")
	// ErrNoJournals is a command line that names no journal file.
	ErrNoJournals = errors.New("no .snap or .xlog files found")
)

// String names the kind as its files are named.
func (k Kind) String() string {
	switch k {
	case KindSnap:
		return "snapshot"
	case KindXlog:
		return "write-ahead log"
	case KindVylog:
		return "vinyl log"
	}

	return fmt.Sprintf("Kind(%d)", int(k))
}

// kindOf returns the kind of a file named name, and false when the name is
// not that of a journal file.
func kindOf(name string) (Kind, bool) {
	switch filepath.Ext(name) {
	case extSnap:
		return KindSnap, true
	case extXlog:
		return KindXlog, true
	case extVylog:
		return KindVylog, true
	}

	return 0, false
}

// File is a journal file with its header.
type File struct {
	// Path is the absolute path of the file.
	Path string
	// Kind is what the file holds.
	Kind Kind
	// Meta is the header of the file. Its VClock is where a write-ahead
	// log starts and the state a snapshot holds.
	Meta *format.Meta
}

// Collect returns the journal files paths name, ordered by their vclocks.
//
// A file is taken as it is: a .snap, .xlog or .vylog file, and ErrNotJournal
// otherwise. A directory contributes its .snap and .xlog files, those of its
// subdirectories too when recursive is set; a .vylog file is read only when
// it is named. A file named twice, by itself or through a directory, is
// taken once. A path that does not exist is an error, as is a command line
// that comes to no file at all.
//
// The files are ordered by the signatures of their vclocks - the sum of the
// LSNs - which orders the files of one instance as it wrote them; at one
// signature a snapshot goes before a write-ahead log, which goes before a
// vinyl log.
func Collect(paths []string, recursive bool) ([]*File, error) {
	seen := map[string]bool{}

	var files []*File

	for _, path := range paths {
		found, err := collectPath(path, recursive)
		if err != nil {
			return nil, err
		}

		if len(found) == 0 {
			log.Warnf("No .snap or .xlog files found in %s", path)
		}

		for _, file := range found {
			if seen[file] {
				continue
			}

			seen[file] = true

			kind, _ := kindOf(file)

			meta, err := reader.ReadHeader(file, reader.AcceptV012())
			if err != nil {
				return nil, fmt.Errorf("reading the header of %s: %w", file, err)
			}

			files = append(files, &File{Path: file, Kind: kind, Meta: meta})
		}
	}

	if len(files) == 0 {
		return nil, ErrNoJournals
	}

	slices.SortStableFunc(files, func(a, b *File) int {
		return cmp.Or(
			cmp.Compare(a.Meta.VClock.Signature(), b.Meta.VClock.Signature()),
			cmp.Compare(a.Kind, b.Kind),
			cmp.Compare(a.Path, b.Path),
		)
	})

	return files, nil
}

// collectPath returns the absolute paths of the journal files path names.
func collectPath(path string, recursive bool) ([]string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", path, err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil, err //nolint:wrapcheck // *fs.PathError names the path.
	}

	if !info.IsDir() {
		if _, ok := kindOf(abs); !ok {
			return nil, fmt.Errorf("%w: %s", ErrNotJournal, path)
		}

		return []string{abs}, nil
	}

	var found []string

	err = filepath.WalkDir(abs, func(entryPath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			if entryPath != abs && !recursive {
				return filepath.SkipDir
			}

			return nil
		}

		if kind, ok := kindOf(entry.Name()); ok && kind != KindVylog {
			found = append(found, entryPath)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the directory %s: %w", path, err)
	}

	return found, nil
}
