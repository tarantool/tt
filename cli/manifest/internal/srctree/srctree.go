// Package srctree walks a project directory the way git sees it: what the
// .gitignore files ignore is left out, the rules of each directory applying
// to its own subtree. tt hashes such a walk where a hash has to follow the
// sources and not what a build generates beside them - the inputs of an
// incremental component (cli/manifest/build) and the content of a path
// dependency that holds a manifest (cli/manifest/resolve).
//
// Only the .gitignore files of the walked tree are read: neither
// .git/info/exclude nor the global excludes of a user, which differ from one
// checkout to the next, so the same tree walks the same everywhere.
package srctree

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// IgnoreFile holds the ignore rules of its directory and below.
const IgnoreFile = ".gitignore"

// Ancestors reads the ignore rules of root and of the directories below it on
// the way to the one at domain, which is left out: Walk reads its own. A walk
// of a subdirectory so honours what the directories above it ignore, as git
// does.
func Ancestors(root string, domain []string) ([]gitignore.Pattern, error) {
	var patterns []gitignore.Pattern

	dir := root

	for idx := range domain {
		own, err := readIgnores(dir, domain[:idx])
		if err != nil {
			return nil, err
		}

		patterns = append(patterns, own...)
		dir = filepath.Join(dir, domain[idx])
	}

	return patterns, nil
}

// readIgnores reads the .gitignore of dir, the directory at domain.
func readIgnores(dir string, domain []string) ([]gitignore.Pattern, error) {
	data, err := os.ReadFile(filepath.Join(dir, IgnoreFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading the ignore rules of %s: %w", dir, err)
	}

	var patterns []gitignore.Pattern

	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}

		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}

	return patterns, nil
}

// Skip tells Walk to leave out the entry at path, the slash-separated
// components of its path from the root of the walk, before the ignore rules
// are asked. A directory it names is not descended.
type Skip func(path []string, entry fs.DirEntry) bool

// Visit is called for every entry of a walk that is not a directory - a
// regular file, a symbolic link or anything else, which entry tells apart -
// with its path on disk and the slash-separated components of its path from
// the root of the walk.
type Visit func(full string, path []string, entry fs.DirEntry) error

// Walk visits the entries under dir, the directory at domain, in the order of
// their names, a directory's entries where the directory falls in that
// order. An entry skip names is left out, and so is one the ignore rules
// ignore: inherited, which hold for dir (Ancestors), then those of dir and of
// each directory below for its own subtree. A symbolic link is visited, never
// followed. An error of visit stops the walk and is returned as it is.
func Walk(
	dir string, domain []string, inherited []gitignore.Pattern, skip Skip, visit Visit,
) error {
	own, err := readIgnores(dir, domain)
	if err != nil {
		return err
	}

	patterns := slices.Concat(inherited, own)
	matcher := gitignore.NewMatcher(patterns)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}

	for _, entry := range entries {
		path := slices.Concat(domain, []string{entry.Name()})

		if skip != nil && skip(path, entry) {
			continue
		}

		if matcher.Match(path, entry.IsDir()) {
			continue
		}

		full := filepath.Join(dir, entry.Name())

		if entry.IsDir() {
			err = Walk(full, path, patterns, skip, visit)
		} else {
			err = visit(full, path, entry)
		}

		if err != nil {
			return err
		}
	}

	return nil
}
