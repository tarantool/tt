package restore

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/cli/backup/archive"
)

// vinylTreeExts are the vinyl files Tarantool keeps in a tree of its own:
// every run and index of a space sits in <space_id>/<index_id>/ under
// vinyl.dir, and that pair is the part of an entry's name a restore has to
// preserve. Every other kind of file lies flat in its directory.
var vinylTreeExts = []string{".run", ".index"}

// vinylTreeDepth is how many path elements a vinyl run or index is named by:
// the space id, the index id and the file itself.
const vinylTreeDepth = 3

// inProgressExt is what Tarantool appends to the name of a file it is still
// writing, on top of the extension of the finished form. The shape of an entry
// name is read off the extension underneath it, so that an interrupted vinyl
// run keeps the <space_id>/<index_id>/ place a finished one keeps.
const inProgressExt = ".inprogress"

// canonicalEntryName returns the name an archive entry lands under, relative
// to the directory of its own kind.
//
// An archive names an entry relative to the data directory holding files of
// its kind, but an archive taken from an instance that nests one data
// directory inside another can carry the other directory's name in front --
// "vinyl/512/0/<n>.run" when vinyl.dir sits under wal.dir, "snap/<n>.snap"
// when snapshot.dir does. Unpacking such a name verbatim would put the file
// one level too deep under the directory it is routed to, where the instance
// does not look for it. Only the place a file occupies inside its own
// directory is therefore kept: the base name for every kind but the vinyl runs
// and indexes, which keep the <space_id>/<index_id>/ pair Tarantool indexes
// them by.
//
// A name that is not one of those two shapes is refused rather than guessed
// at, and refused before anything is written: the two directory elements of a
// vinyl run are a space and an index id, so anything else in their place means
// the entry's own directory cannot be told from the one in front of it, and
// putting it somewhere plausible would leave the instance replaying a file
// from a tree it does not belong to.
func canonicalEntryName(name string) (string, error) {
	if err := archive.CheckEntryName(name); err != nil {
		return "", fmt.Errorf("%w: %w", ErrValidation, err)
	}

	// A tar entry name is slash-separated whatever the host it was packed on.
	elems := strings.Split(name, "/")
	base := elems[len(elems)-1]

	if !slices.Contains(vinylTreeExts, filepath.Ext(strings.TrimSuffix(base, inProgressExt))) {
		return base, nil
	}

	if len(elems) == 1 {
		return base, nil
	}

	if len(elems) < vinylTreeDepth {
		return "", vinylNameError(name) //nolint:wrapcheck
	}

	space, index := elems[len(elems)-3], elems[len(elems)-2]
	if !isDecimal(space) || !isDecimal(index) {
		return "", vinylNameError(name) //nolint:wrapcheck
	}

	return space + "/" + index + "/" + base, nil
}

// vinylNameError refuses an entry whose name does not place a vinyl file in a
// space and index of its own.
func vinylNameError(name string) error {
	return fmt.Errorf("%w: archive entry %q: a vinyl run or index is named "+
		"<space_id>/<index_id>/<file>", ErrValidation, name)
}

// isDecimal reports whether s is a non-empty run of decimal digits. Tarantool
// names the directories of a vinyl tree after numeric ids, so this is what
// tells them from a data directory's name standing in front of them.
func isDecimal(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}
