package restore

import (
	"path/filepath"
	"slices"
	"sort"

	"github.com/tarantool/tt/cli/backup/archive"
)

// sortDataExt is the memtx sort data Tarantool writes beside a snapshot of the
// same signature.
const sortDataExt = ".sortdata"

// Layout is the set of directories a restore rebuilds: one per kind of file
// Tarantool keeps. A 3.x deployment routinely puts the journal on a different
// device from the snapshots and the vinyl data, and every file has to come
// back into the directory the instance's configuration names for it --
// otherwise the instance starts on whichever part of the restore it can see,
// which is a state nobody asked for and which looks healthy.
type Layout struct {
	// Snapshot is snapshot.dir: the .snap files.
	Snapshot string
	// WAL is wal.dir: the .xlog files.
	WAL string
	// Vinyl is vinyl.dir: the .vylog and the run/index files under their
	// <space_id>/<index_id>/ subdirectories.
	Vinyl string
}

// FlatLayout puts every kind of file in one directory. It is the layout of an
// instance whose configuration sets none of the three keys, and the one a
// caller that only knows a single directory asks for.
func FlatLayout(dir string) Layout {
	return Layout{Snapshot: dir, WAL: dir, Vinyl: dir}
}

// DirFor returns the directory an archive entry belongs in. The kind of the
// file decides it, because Tarantool writes each kind into one directory only:
// snapshots and the sort data beside them into snapshot.dir, journals into
// wal.dir, and the .vylog together with the numbered run/index files into
// vinyl.dir. An entry Tarantool did not write -- an archive can carry anything
// -- goes with the vinyl data rather than being dropped, so that a restore
// never silently loses a file the backup kept.
//
// The classification is the one the backup names entries by, so that a file
// packed as one kind is unpacked as the same one.
func (l Layout) DirFor(entryName string) string {
	switch archive.KindOf(entryName) {
	case archive.KindSnapshot:
		return l.Snapshot
	case archive.KindWAL:
		return l.WAL
	default:
		return l.Vinyl
	}
}

// Resolved returns the layout with every directory made absolute and cleaned.
//
// A restore resolves once and then works on the result, so that the directory
// it routes into, the directory it sweeps, the directory it anchors the marker
// on and the directory it reports are the same string. Two spellings of one
// directory would otherwise be swept twice, reported as two, and recorded in
// the marker in a form that means nothing away from the process's working
// directory.
//
// Symbolic links are left alone: the marker and the report name the directory
// the operator named, which is what they will compare the restore against, and
// a path resolved through its links is a different answer to that question.
// Where two names have to be told apart as one place -- the sweep, which must
// not prune a directory of the layout it reached under another name -- the
// filesystem is asked instead.
func (l Layout) Resolved() Layout {
	return Layout{
		Snapshot: resolveDir(l.Snapshot),
		WAL:      resolveDir(l.WAL),
		Vinyl:    resolveDir(l.Vinyl),
	}
}

// dataDirs returns the layout's directories, resolved and without repeats,
// the deepest first.
//
// The order is what makes cleanup work on a nested layout. Cleanup prunes a
// directory it empties, so sweeping a parent would take an emptied child away
// with it; sweeping the child first leaves the parent's pass nothing to
// surprise it with. Resolving before the comparison is what makes two
// spellings of one directory -- "." and the path it stands for -- count once.
func (l Layout) dataDirs() []string {
	resolved := l.Resolved()
	dirs := make([]string, 0, 3)

	for _, dir := range []string{resolved.Snapshot, resolved.WAL, resolved.Vinyl} {
		if !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}

	// A nested directory's path is strictly longer than the path of the
	// directory it sits in, so length orders children before their parents.
	sort.SliceStable(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })

	return dirs
}

// resolveDir makes a directory path absolute, falling back on the cleaned name
// when the working directory cannot be read.
func resolveDir(dir string) string {
	resolved, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Clean(dir)
	}

	return resolved
}
