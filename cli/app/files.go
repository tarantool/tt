package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/tarantool/tt/sdk/integrity"
)

// migrationGuide is the guide that explains moving from tt2.
const migrationGuide = "doc/migration_from_older_versions.md"

// tt2Files are the files a tt2 environment is recognised by: its config, in
// either spelling tt2 accepted, and the instances file of its application
// directories. tt reads neither any more. instances.yaml, which tt2 accepted
// as well, is left out: it is also the name of a pair's description, and a
// directory holding the pair of a host is no tt2 environment.
var tt2Files = []string{"tt.yaml", "tt.yml", "instances.yml"}

// withTT2Hint adds to err, a failure to find the application, that start is
// in a tt2 environment, when it is: start or one of its parents holds a file
// of tt2. The caller has already found that none of them is a project.
//
// Without it a user of tt2 sees that there is no project where, to them,
// there plainly is one. The files are only looked for, never read: their
// content means nothing to tt now, and acting on it is what dropping
// tt.yaml took away.
func withTT2Hint(err error, start string) error {
	path, found := findTT2File(start)
	if !found {
		return err
	}

	return fmt.Errorf("%w; found %s: %w, and tt no longer reads tt.yaml and "+
		"instances.yml; see %s", err, path, ErrTT2Environment, migrationGuide)
}

// findTT2File returns the nearest file of tt2 in start or its parents. A
// failure to look counts as absence: this only adds a hint to an error.
func findTT2File(start string) (string, bool) {
	dir := start

	for {
		for _, name := range tt2Files {
			path := filepath.Join(dir, name)

			found, err := exists(path)
			if found && err == nil {
				return path, true
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}

		dir = parent
	}
}

// exists reports whether there is a directory entry at path, whatever its
// type: a dangling link counts, so that it is reported when it is read
// rather than taken for a file nobody wrote. Any failure to tell, other than
// the entry being absent, is an error.
func exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}

	if isAbsent(err) {
		return false, nil
	}

	return false, fmt.Errorf("cannot look for %s: %w", path, err)
}

// isAbsent reports whether err says there is nothing at the path: either the
// entry is missing or one of the path's parents is not a directory.
func isAbsent(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// readFile reads the file at path through the integrity repository, so that
// with integrity checks on, the bytes parsed are the bytes checked. A
// context without a repository checks nothing, as cluster.GetClusterConfig
// treats one.
func readFile(integ integrity.IntegrityCtx, path string) ([]byte, error) {
	if integ.Repository == nil {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("cannot read %s: %w", path, err)
		}

		return data, nil
	}

	data, err := integ.Repository.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}

	return data, nil
}
