package emmylua

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// luaExtensions are the extensions, in lower case, of the files luafmt
// formats.
//
//nolint:gochecknoglobals // A constant list: Go has no constant slices.
var luaExtensions = []string{".lua", ".luau"}

// unwalkedDirs are the directories luafmt does not walk into, wherever they
// are, hidden or not.
//
//nolint:gochecknoglobals // A constant list: Go has no constant slices.
var unwalkedDirs = []string{".git", ".hg", ".svn", "node_modules", "target"}

// ownDirs are the directories of tt at the root of a project, which are not
// its code even when the project tracks them: the build tree, where tt builds
// the project out of its sources, the rock tree and the files of this
// machine. EmmyLua's configuration ignores them too.
//
//nolint:gochecknoglobals // A constant list: Go has no constant slices.
var ownDirs = []string{"_build", ".rocks", ".tarantool"}

// walkOptions are the flags of luafmt, among the arguments after --, that
// change which files its walk of a directory takes, so a list of files
// takes them alike.
type walkOptions struct {
	// hidden is --include-hidden: files and directories whose name starts
	// with a dot.
	hidden bool
	// symlinks is --follow-symlinks: a link to a regular file.
	symlinks bool
}

// walkOf returns the walkOptions of pass, the arguments of luafmt.
func walkOf(pass []string) walkOptions {
	return walkOptions{
		hidden:   slices.Contains(pass, "--include-hidden"),
		symlinks: slices.Contains(pass, "--follow-symlinks"),
	}
}

// gitLuaFiles returns the Lua files under dir that git sees - tracked, and
// untracked but not ignored by .gitignore, .git/info/exclude or
// core.excludesFile - relative to dir, sorted, as luaFiles picks them, and
// whether git chooses the files of dir at all: false when git is not
// installed, dir is not in a work tree of git, or git ignores dir itself.
// The files of a submodule are left out: it is another repository, a
// directory to git, and its own style is not the project's to apply.
func (r *runner) gitLuaFiles(
	ctx context.Context, dir string, walk walkOptions,
) ([]string, bool, error) {
	logger := r.services.Log()

	git, err := exec.LookPath("git")
	if err != nil {
		logger.Debug("no git to list the Lua files of " + dir + " (" + err.Error() +
			"): luafmt walks it")

		return nil, false, nil //nolint:nilerr // No git is a directory out of git.
	}

	out, err := runGit(ctx, git, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		logger.Debug(dir + " is not in a work tree of git (" + err.Error() + "): luafmt walks it")

		return nil, false, nil //nolint:nilerr // git fails so out of a work tree.
	}

	if strings.TrimSpace(string(out)) != "true" {
		logger.Debug(dir + " is not in a work tree of git: luafmt walks it")

		return nil, false, nil
	}

	_, err = runGit(ctx, git, dir, "check-ignore", "-q", ".")

	exitErr, failed := errors.AsType[*exec.ExitError](err)

	switch {
	case err == nil:
		logger.Debug("git ignores " + dir + ": luafmt walks it")

		return nil, false, nil
	case failed && exitErr.ExitCode() == 1:
		// Not ignored.
	default:
		return nil, false, fmt.Errorf("asking git whether it ignores %s: %w", dir, err)
	}

	out, err = runGit(ctx, git, dir, "ls-files", "-z", "--cached", "--others",
		"--exclude-standard")
	if err != nil {
		return nil, false, fmt.Errorf("listing the files git sees in %s: %w", dir, err)
	}

	var paths []string

	for entry := range bytes.SplitSeq(out, []byte{0}) {
		if len(entry) > 0 {
			paths = append(paths, string(entry))
		}
	}

	return luaFiles(dir, paths, walk), true, nil
}

// runGit runs git with args in dir and returns what it prints. A failure
// carries what git said on stderr.
func runGit(ctx context.Context, git, dir string, args ...string) ([]byte, error) {
	var stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, git, append([]string{"-C", dir}, args...)...)

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("git %s: %w: %s", args[0], err, msg)
		}

		return out, fmt.Errorf("git %s: %w", args[0], err)
	}

	return out, nil
}

// luaFiles returns, sorted and once each, the paths among paths - relative
// to dir, with slashes, as git lists them - that luafmt's walk of dir with
// walk would take: regular files with a Lua extension, outside a hidden
// file or directory and outside unwalkedDirs, a link only with
// walk.symlinks. Those under ownDirs are left out, and so are those the work
// tree no longer has, and the directories git lists: submodules and the
// repositories inside the work tree.
func luaFiles(dir string, paths []string, walk walkOptions) []string {
	paths = slices.Compact(slices.Sorted(slices.Values(paths)))

	files := make([]string, 0, len(paths))

	for _, name := range paths {
		if !slices.Contains(luaExtensions, strings.ToLower(path.Ext(name))) {
			continue
		}

		parts := strings.Split(name, "/")
		if slices.Contains(ownDirs, parts[0]) || !walked(parts, walk) {
			continue
		}

		file := filepath.FromSlash(name)
		if regularFile(filepath.Join(dir, file), walk.symlinks) {
			files = append(files, file)
		}
	}

	return files
}

// walked reports whether luafmt walks into the path of parts, the names of
// its directories and its file: none is hidden, unless walk.hidden, and no
// directory is among unwalkedDirs.
func walked(parts []string, walk walkOptions) bool {
	for i, part := range parts {
		if !walk.hidden && strings.HasPrefix(part, ".") {
			return false
		}

		if i < len(parts)-1 && slices.Contains(unwalkedDirs, part) {
			return false
		}
	}

	return true
}

// regularFile reports whether path is a regular file, or with symlinks a
// link to one.
func regularFile(path string, symlinks bool) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}

	if info.Mode()&os.ModeSymlink != 0 {
		if !symlinks {
			return false
		}

		info, err = os.Stat(path)
		if err != nil {
			return false
		}
	}

	return info.Mode().IsRegular()
}
