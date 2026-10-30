package distbuild

import (
	"errors"
	"fmt"
	"go/build"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

const (
	// ignoreFile holds the ignore rules of a directory and its subtree.
	ignoreFile = ".gitignore"
	// gitDir marks the root of a git repository: a directory, or a file in
	// a worktree or a submodule.
	gitDir = ".git"
	// goMod is the file a module is defined by, and goSum the checksums of
	// its dependencies.
	goMod = "go.mod"
	goSum = "go.sum"
	// embedAll prefixes a //go:embed pattern whose directories are embedded
	// with the names that start with . or _.
	embedAll = "all:"
)

// vcsDirs are the directories of version control systems, which no module
// copy carries, nor the .git file of a worktree or a submodule, which names
// a directory of the machine.
var vcsDirs = []string{gitDir, ".hg", ".svn", ".bzr"}

// sourceExts are the extensions of the files the go command builds a
// package from: the Go, cgo and assembly sources and the objects it links.
var sourceExts = []string{
	".go", ".c", ".h", ".cc", ".cpp", ".cxx", ".hh", ".hpp", ".hxx", ".m",
	".s", ".S", ".sx", ".f", ".F", ".for", ".f90", ".syso",
}

// moduleFile is a file of a module.
type moduleFile struct {
	// rel is its slash-separated path in the module.
	rel string
	// path is the file.
	path string
}

// moduleFiles lists the files of the module in dir that are its content:
// the ones its content hash covers and its copy in a bundle carries. They
// are its regular files - symbolic links and other files are left out -
// but the .git file of a worktree, those of the directories skipModuleDir
// skips and those .gitignore ignores.
//
// The .gitignore files are the module's own, each for its subtree, and
// those of the directories above it up to the root of its git repository,
// the nearest one with a .git, or, outside a repository, up to manifestDir
// if the module is in it. The deeper and the later pattern wins, and a file
// in an ignored directory stays ignored, as in git. They are read without
// git, so that a tarball of a checkout lists what the checkout does, and
// the excludes of a machine - core.excludesFile, .git/info/exclude - are
// not read, so that every machine lists the same files. The files the go
// command may build the module from are listed even when ignored, in an
// ignored directory too: the Go and cgo sources (see sourceExts), go.mod,
// go.sum and the files the //go:embed directives of its packages embed
// under any build constraint. work is the directory of the build, which is
// no module's.
func moduleFiles(dir, manifestDir, work string) ([]moduleFile, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dir, err)
	}

	top, err := filepath.Abs(manifestDir)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dir, err)
	}

	base := ignoreBase(root, top)

	rel, err := filepath.Rel(base, root)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dir, err)
	}

	var domain []string

	if rel != "." {
		domain = strings.Split(filepath.ToSlash(rel), "/")
	}

	patterns, ignored, err := ancestorPatterns(base, domain)
	if err != nil {
		return nil, err
	}

	walk := fileWalk{domain: domain, work: work, embedded: map[string]bool{}, files: nil}

	err = walk.dir(root, "", patterns, ignored)
	if err != nil {
		return nil, err
	}

	return walk.files, nil
}

// ignoreBase returns the directory the ignore rules of the module in dir
// are read from: the root of its git repository, else manifestDir if the
// module is in it, else dir.
func ignoreBase(dir, manifestDir string) string {
	for parent := dir; ; parent = filepath.Dir(parent) {
		_, err := os.Lstat(filepath.Join(parent, gitDir))
		if err == nil {
			return parent
		}

		if filepath.Dir(parent) == parent {
			break
		}
	}

	rel, err := filepath.Rel(manifestDir, dir)
	if err == nil && filepath.IsLocal(rel) {
		return manifestDir
	}

	return dir
}

// ancestorPatterns reads the ignore rules of the directories from base down
// to the module, whose path in base is domain, and reports whether they
// ignore the module itself: a directory on the way to it, or it.
func ancestorPatterns(base string, domain []string) ([]gitignore.Pattern, bool, error) {
	var patterns []gitignore.Pattern

	dir := base

	for idx, name := range domain {
		own, err := readIgnoreFile(filepath.Join(dir, ignoreFile), domain[:idx])
		if err != nil {
			return nil, false, err
		}

		patterns = append(patterns, own...)
		dir = filepath.Join(dir, name)

		if gitignore.NewMatcher(patterns).Match(domain[:idx+1], true) {
			return nil, true, nil
		}
	}

	return patterns, false, nil
}

// readIgnoreFile reads the patterns of the .gitignore file name, of the
// directory whose path in the base of the rules is domain. A missing file,
// or one that is not a regular file - git reads no symbolic link there -
// has none.
func readIgnoreFile(name string, domain []string) ([]gitignore.Pattern, error) {
	info, err := os.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}

	if !info.Mode().IsRegular() {
		return nil, nil
	}

	data, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}

	var patterns []gitignore.Pattern

	text := strings.TrimPrefix(string(data), "\ufeff")

	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}

		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}

	return patterns, nil
}

// fileWalk lists the files of a module.
type fileWalk struct {
	// domain is the path of the module in the base of its ignore rules.
	domain []string
	// work is the directory of the build.
	work string
	// embedded are the files the packages walked so far embed, by their
	// paths in the module.
	embedded map[string]bool
	// files are the files listed so far.
	files []moduleFile
}

// dir lists the files of the directory dir, whose path in the module is
// rel, under the ignore rules patterns of the directories above it. An
// ignored directory is walked for the files listed even when ignored.
func (w *fileWalk) dir(dir, rel string, patterns []gitignore.Pattern, ignored bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}

	if !ignored {
		own, readErr := readIgnoreFile(filepath.Join(dir, ignoreFile), w.path(rel))
		if readErr != nil {
			return readErr
		}

		patterns = append(slices.Clip(patterns), own...)
	}

	matcher := gitignore.NewMatcher(patterns)

	if slices.ContainsFunc(entries, isGoFile) {
		w.embed(dir, rel)
	}

	for _, entry := range entries {
		name := path.Join(rel, entry.Name())
		file := filepath.Join(dir, entry.Name())

		if entry.IsDir() {
			if skipModuleDir(file, entry, w.work) {
				continue
			}

			err = w.dir(file, name, patterns, ignored || matcher.Match(w.path(name), true))
			if err != nil {
				return err
			}

			continue
		}

		if !entry.Type().IsRegular() || slices.Contains(vcsDirs, entry.Name()) {
			continue
		}

		listed := !ignored && !matcher.Match(w.path(name), false)
		if listed || buildFile(entry.Name()) || w.embedded[name] {
			w.files = append(w.files, moduleFile{rel: name, path: file})
		}
	}

	return nil
}

// path is the path of the file rel of the module in the base of its
// ignore rules, as a matcher takes it.
func (w *fileWalk) path(rel string) []string {
	if rel == "" {
		return w.domain
	}

	return append(slices.Clone(w.domain), strings.Split(rel, "/")...)
}

// embed records the files the package in dir, whose path in the module is
// rel, embeds, as the go command resolves its //go:embed patterns: from
// the directory of the package, a directory with every file under it but
// those of nested modules, of VCS directories and, without all:, those
// whose names start with . or _. The patterns are those of every file of
// the package whatever its build constraints: the build may set any. Those
// of its tests are not: no test is built into the binary, nor vendored.
// What the go command would refuse to embed is left to the build to
// report.
func (w *fileWalk) embed(dir, rel string) {
	ctxt := build.Default

	ctxt.UseAllFiles, ctxt.CgoEnabled = true, true

	// A package that does not build still names what it embeds.
	pkg, _ := ctxt.ImportDir(dir, 0)
	if pkg == nil {
		return
	}

	for _, pattern := range pkg.EmbedPatterns {
		glob, all := strings.CutPrefix(pattern, embedAll)
		if glob == "." || !fs.ValidPath(glob) {
			continue
		}

		matches, err := fs.Glob(os.DirFS(dir), glob)
		if err != nil {
			continue
		}

		for _, match := range matches {
			w.embedMatch(dir, rel, match, all)
		}
	}
}

// embedMatch records the files match, a path in the directory dir of a
// package whose path in the module is rel, embeds: the file, or the files
// of the directory.
func (w *fileWalk) embedMatch(dir, rel, match string, all bool) {
	root := filepath.Join(dir, filepath.FromSlash(match))

	_ = filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if name != root && skipEmbedName(entry.Name(), all) {
			if entry.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}

		if entry.IsDir() {
			_, statErr := os.Stat(filepath.Join(name, goMod))
			if statErr == nil {
				return filepath.SkipDir
			}

			return nil
		}

		if entry.Type().IsRegular() {
			file, relErr := filepath.Rel(dir, name)
			if relErr != nil {
				return fmt.Errorf("embedding %s: %w", name, relErr)
			}

			w.embedded[path.Join(rel, filepath.ToSlash(file))] = true
		}

		return nil
	})
}

// skipEmbedName reports whether the go command leaves the file or the
// directory name out of a directory it embeds.
func skipEmbedName(name string, all bool) bool {
	if slices.Contains(vcsDirs, name) {
		return true
	}

	return !all && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_"))
}

// isGoFile reports whether entry is a Go source file the go command reads.
func isGoFile(entry fs.DirEntry) bool {
	name := entry.Name()

	return !entry.IsDir() && strings.HasSuffix(name, ".go") &&
		!strings.HasPrefix(name, ".") && !strings.HasPrefix(name, "_")
}

// buildFile reports whether the file name is one the go command may build
// a module from whatever its directory: a source, go.mod or go.sum.
func buildFile(name string) bool {
	return name == goMod || name == goSum || slices.Contains(sourceExts, path.Ext(name))
}

// skipModuleDir reports whether the directory dir met walking a module
// holds none of its files: a VCS directory, a vendor directory, a nested
// module - a directory with a go.mod of its own - or work, the directory
// of the build. These are the directories the go command leaves out of a
// module zip, and work is not the module's at all.
func skipModuleDir(dir string, entry fs.DirEntry, work string) bool {
	if slices.Contains(vcsDirs, entry.Name()) || entry.Name() == "vendor" || dir == work {
		return true
	}

	_, err := os.Stat(filepath.Join(dir, goMod))

	return err == nil
}
