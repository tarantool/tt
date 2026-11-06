package data

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

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
	// KindIndex is the index of a vinyl run: the info of the run and of its
	// pages.
	KindIndex
	// KindRun is a vinyl run: the statements of one index of a vinyl space,
	// in the order of their keys. It is the data of the space, not a
	// journal of its changes.
	KindRun
)

// Extensions of the journal files, which name their kinds.
const (
	extSnap  = ".snap"
	extXlog  = ".xlog"
	extVylog = ".vylog"
	extRun   = ".run"
	extIndex = ".index"
)

var (
	// ErrNotJournal is a file named on the command line that is not a
	// journal file.
	ErrNotJournal = errors.New("not a journal file")
	// ErrNoJournals is a command line that names no journal file.
	ErrNoJournals = errors.New("no journal files found")
	// ErrVinylRun is a .run or .index file given to a command that does not
	// read them.
	ErrVinylRun = errors.New("a vinyl run is not a journal")
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
	case KindIndex:
		return "vinyl run index"
	case KindRun:
		return "vinyl run"
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
	case extRun:
		return KindRun, true
	case extIndex:
		return KindIndex, true
	}

	return 0, false
}

// isVinylRun reports whether kind is a file of a vinyl run.
func isVinylRun(kind Kind) bool {
	return kind == KindRun || kind == KindIndex
}

// LSM is the LSM tree a vinyl run belongs to, as its path names it:
// <vinyl_dir>/<space_id>/<index_id>/<run_id>.run, and .index for its index.
type LSM struct {
	// SpaceID and IndexID are the space and the index of the LSM tree.
	SpaceID, IndexID uint32
	// RunID is the id of the run.
	RunID uint64
}

// lsmOf returns the LSM tree of the vinyl run at path, nil when the path does
// not name one.
func lsmOf(path string) *LSM {
	name := filepath.Base(path)

	runID, err := strconv.ParseUint(strings.TrimSuffix(name, filepath.Ext(name)), 10, 64)
	if err != nil {
		return nil
	}

	indexDir := filepath.Dir(path)

	indexID, err := strconv.ParseUint(filepath.Base(indexDir), 10, 32)
	if err != nil {
		return nil
	}

	spaceID, err := strconv.ParseUint(filepath.Base(filepath.Dir(indexDir)), 10, 32)
	if err != nil {
		return nil
	}

	return &LSM{SpaceID: uint32(spaceID), IndexID: uint32(indexID), RunID: runID}
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
	// LSM is the LSM tree of a vinyl run, by its path; nil for another
	// file, or a run whose path does not name its space and index.
	LSM *LSM
}

// CollectOptions are what Collect takes.
type CollectOptions struct {
	// Recursive takes the files of the subdirectories of a directory too.
	Recursive bool
	// Runs takes vinyl runs: the .run and .index files named, and those of
	// the subdirectories of a directory with Recursive. Without it a .run
	// or .index file named is ErrVinylRun.
	Runs bool
}

// Collect returns the journal files paths name, ordered by their vclocks.
//
// A file is taken as it is: a .snap, .xlog or .vylog file - and a .run or
// .index file with opts.Runs - and ErrNotJournal otherwise. A directory
// contributes its .snap and .xlog files, and its .run and .index files with
// opts.Runs, those of its subdirectories too with opts.Recursive: the runs of
// a vinyl directory are in its <space_id>/<index_id> subdirectories. A .vylog
// file is read only when it is named. A file
// named twice, by itself or through a directory, is taken once. A path that
// does not exist is an error, as is a command line that comes to no file at
// all.
//
// The files are ordered by the signatures of their vclocks - the sum of the
// LSNs - which orders the files of one instance as it wrote them; at one
// signature a snapshot goes before a write-ahead log, which goes before a
// vinyl log. The vinyl runs, which have no vclock, go after them, by space,
// index and run, the index of a run before the run.
func Collect(paths []string, opts CollectOptions) ([]*File, error) {
	seen := map[string]bool{}

	var files []*File

	for _, path := range paths {
		found, err := collectPath(path, opts)
		if err != nil {
			return nil, err
		}

		if len(found) == 0 {
			log.Warnf("No journal files found in %s", path)
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

			entry := &File{Path: file, Kind: kind, Meta: meta, LSM: nil}
			if isVinylRun(kind) {
				entry.LSM = lsmOf(file)
			}

			files = append(files, entry)
		}
	}

	if len(files) == 0 {
		return nil, ErrNoJournals
	}

	slices.SortStableFunc(files, compareFiles)

	return files, nil
}

// compareFiles orders the files as Collect returns them.
func compareFiles(left, right *File) int {
	order := cmp.Compare(boolInt(isVinylRun(left.Kind)), boolInt(isVinylRun(right.Kind)))
	if order != 0 {
		return order
	}

	if !isVinylRun(left.Kind) {
		return cmp.Or(
			cmp.Compare(left.Meta.VClock.Signature(), right.Meta.VClock.Signature()),
			cmp.Compare(left.Kind, right.Kind),
			cmp.Compare(left.Path, right.Path),
		)
	}

	if left.LSM == nil || right.LSM == nil {
		return cmp.Or(cmp.Compare(boolInt(left.LSM == nil), boolInt(right.LSM == nil)),
			cmp.Compare(left.Path, right.Path))
	}

	return cmp.Or(
		cmp.Compare(left.LSM.SpaceID, right.LSM.SpaceID),
		cmp.Compare(left.LSM.IndexID, right.LSM.IndexID),
		cmp.Compare(left.LSM.RunID, right.LSM.RunID),
		cmp.Compare(left.Kind, right.Kind),
		cmp.Compare(left.Path, right.Path),
	)
}

func boolInt(b bool) int {
	if b {
		return 1
	}

	return 0
}

// notJournal is the error of a file named that is not one opts takes.
func notJournal(path string, opts CollectOptions) error {
	if opts.Runs {
		return fmt.Errorf("%w: want a .snap, .xlog, .vylog, .run or .index file: %s",
			ErrNotJournal, path)
	}

	return fmt.Errorf("%w: want a .snap, .xlog or .vylog file: %s", ErrNotJournal, path)
}

// collectPath returns the absolute paths of the journal files path names.
func collectPath(path string, opts CollectOptions) ([]string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", path, err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil, err //nolint:wrapcheck // *fs.PathError names the path.
	}

	if !info.IsDir() {
		kind, ok := kindOf(abs)

		switch {
		case !ok:
			return nil, notJournal(path, opts)
		case isVinylRun(kind) && !opts.Runs:
			return nil, fmt.Errorf("%w: %s holds the statements of an index of a vinyl "+
				"space, in the order of their keys, not the transactions that made them; "+
				"read it with tt data cat", ErrVinylRun, path)
		}

		return []string{abs}, nil
	}

	var found []string

	err = filepath.WalkDir(abs, func(entryPath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			if entryPath != abs && !opts.Recursive {
				return filepath.SkipDir
			}

			return nil
		}

		kind, ok := kindOf(entry.Name())

		switch {
		case !ok, kind == KindVylog:
		case isVinylRun(kind):
			if opts.Runs {
				found = append(found, entryPath)
			}
		default:
			found = append(found, entryPath)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the directory %s: %w", path, err)
	}

	return found, nil
}
