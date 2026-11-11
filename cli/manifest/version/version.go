// Package version derives a package version from a git tree and an optional
// VERSION file, and renders the version.lua descriptor that the build drops
// into a package.
//
// It only derives the version string and generates version.lua; dependency
// resolution and the actual build live elsewhere.
package version

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	goversion "github.com/hashicorp/go-version"
)

// tagGlobs are the --match patterns of git describe: the names of the tags
// that may be versions, a v and a digit or a digit first. tagVersion decides
// whether one is.
var tagGlobs = []string{"v[0-9]*", "[0-9]*"}

// semVerRe is SemVer 2.0.0 in full, as semver.org gives it: the form a tag
// without a v has to take to be a version.
var semVerRe = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
	`(-(0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(\.(0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*)?` +
	`(\+[0-9a-zA-Z-]+(\.[0-9a-zA-Z-]+)*)?$`)

// versionFileName is the optional one-line SemVer override that sits next to
// the manifest and takes precedence over git.
const versionFileName = "VERSION"

// lockFileName is excluded from the dirty check: a regenerated lock on an
// otherwise clean release commit must not spuriously mark the version .dirty.
const lockFileName = "app.manifest.lock"

// porcelainPrefix is the width of the two status columns plus a space that
// "git status --porcelain" prints in front of every path.
const porcelainPrefix = 3

// Version is a derived package version.
type Version struct {
	SemVer string // Rendered SemVer, e.g. "1.4.1-dev.3+gabc1234.dirty".
	// Commit is the short sha "abc1234" of the commit the version is of (see
	// Derive), or empty when there is no commit.
	Commit string
	Dirty  bool   // The project had uncommitted changes (lock excluded).
	Flavor string // ce|ee - a build fact, set by the build, not by Derive.
	// Tag is the git tag the version is that of: the version tag on the
	// commit the version is of, with a clean project, named as git has it -
	// v1.2.0 or 1.2.0 - so that a rockspec can name it. Empty for a version
	// after a tag, a dirty project, a VERSION file and the tagless fallback.
	Tag string
}

// versionTag is a tag of the repository whose name is a version (tagVersion).
type versionTag struct {
	name    string             // As git has it: v1.2.0 or 1.2.0.
	commit  string             // The commit it points to, an annotated tag peeled.
	version *goversion.Version // The version the name is.
}

// subtree is the part of its git repository a project is: all of it for a
// project at the root, the directory of the project for one below it.
type subtree struct {
	// commit is the commit the version is of: HEAD at the root, below it the
	// last commit of the history of HEAD that changed the directory, or
	// empty when no commit did.
	commit string
	// pathspec limits git to the directory below the root, and is empty at
	// the root.
	pathspec []string
}

// Derive determines the package version from the git tree at dir and a
// VERSION file in dir, if present.
//
// Source precedence:
//  1. a VERSION file next to the manifest (one line, SemVer 2.0.0);
//  2. the most recent version tag reachable from the commit the version is
//     of (git describe): v1.2.0 or 1.2.0 (tagVersion), a tag of another name
//     passed over;
//  3. a tagless fallback of 0.0.0-dev.<N>+g<sha>, N = commits in the history
//     of that commit.
//
// Of the version tags on one commit, the version is that of the highest
// version, and of v1.2.0 and 1.2.0, that of v1.2.0 (pickTag).
//
// The commit the version is of is HEAD when dir is the root of its
// repository, and every uncommitted change of the tree marks it dirty. A
// project in a subdirectory - one of a monorepo, a path dependency inside the
// repository of its consumer - is versioned by its own subtree: the commit is
// the last one that changed the directory and only a change under it marks
// the version dirty, so neither a commit nor a change elsewhere in the
// repository moves the version. A tag counts for the project on that commit
// or before it, not on a later commit that left the directory alone; to
// release such a project, tag a commit that changes it.
//
// A missing git binary or .git directory is not fatal: derivation falls through
// to the next source rather than panicking.
func Derive(dir string) (Version, error) {
	ver, ok, err := deriveFromFile(dir)
	if err != nil {
		return ver, err
	}

	if ok {
		return ver, nil
	}

	tree := gitSubtree(dir)
	dirty := isDirty(dir, tree.pathspec)

	ver, ok = deriveFromGitDescribe(dir, tree.commit, dirty)
	if ok {
		return ver, nil
	}

	return deriveFallback(dir, tree.commit, dirty), nil
}

// gitSubtree finds the part of its repository the project at dir is. Out of a
// repository, git fails on HEAD as it does at the root of one with no commit.
func gitSubtree(dir string) subtree {
	prefix, err := runGit(dir, "rev-parse", "--show-prefix")
	if err != nil || prefix == "" {
		return subtree{commit: "HEAD", pathspec: nil}
	}

	// The pathspec "." is dir, git running in it. rev-list rather than log:
	// no log.* setting of the user reaches its output.
	pathspec := []string{"--", "."}

	commit, err := runGit(dir, append([]string{"rev-list", "--max-count=1", "HEAD"},
		pathspec...)...)
	if err != nil {
		// No commit in the repository yet.
		commit = ""
	}

	return subtree{commit: commit, pathspec: pathspec}
}

// deriveFromFile reads the VERSION file. The file is an explicit pin: the
// version is taken verbatim (minus a leading "v"), without git metadata or a
// dirty marker. ok is false when the file is absent.
func deriveFromFile(dir string) (Version, bool, error) {
	var ver Version

	raw, err := os.ReadFile(filepath.Join(dir, versionFileName))
	if errors.Is(err, os.ErrNotExist) {
		return ver, false, nil
	}

	if err != nil {
		return ver, false, fmt.Errorf("reading %s: %w", versionFileName, err)
	}

	semver := strings.TrimPrefix(strings.TrimSpace(string(raw)), "v")

	_, err = goversion.NewSemver(semver)
	if err != nil {
		return ver, false, fmt.Errorf("%s: %q is not SemVer 2.0.0: %w",
			versionFileName, semver, err)
	}

	ver.SemVer = semver

	return ver, true, nil
}

// deriveFromGitDescribe runs git describe on commit, the commit the version is
// of, and applies the pre-release / dev cycle rules. ok is false when there is
// no commit or no version tag is reachable from it, in which case the caller
// falls back to deriveFallback.
func deriveFromGitDescribe(dir, commit string, dirty bool) (Version, bool) {
	var ver Version

	if commit == "" {
		return ver, false
	}

	tags, skipped := listTags(dir)
	if len(tags) == 0 {
		return ver, false
	}

	// The globs keep the tags that cannot be versions out of the walk, and the
	// names of those they match but tagVersion refuses (v1.x, 2026-11) are
	// excluded one by one, so the walk goes on to the nearest version tag past
	// them. No tag name holds a glob character. --long always prints the
	// <tag>-<N>-g<sha> form.
	args := []string{"describe", "--tags", "--long"}

	for _, glob := range tagGlobs {
		args = append(args, "--match", glob)
	}

	for _, name := range skipped {
		args = append(args, "--exclude", name)
	}

	out, err := runGit(dir, append(args, commit)...)
	if err != nil {
		return ver, false
	}

	described, count, sha, ok := splitDescribe(out)
	if !ok {
		return ver, false
	}

	tag, ok := pickTag(tags, described)
	if !ok {
		return ver, false
	}

	ver.SemVer = renderDescribed(tag.version, count, sha, dirty)
	ver.Commit = shortSHA(sha)
	ver.Dirty = dirty

	if count == 0 && !dirty {
		ver.Tag = tag.name
	}

	return ver, true
}

// listTags reads the tags of the repository at dir: those whose names are
// versions (tagVersion), by name, and the names of the others tagGlobs match,
// which git describe is to pass over. Both are empty when the repository has
// no tag or there is no repository.
func listTags(dir string) ([]versionTag, []string) {
	// --dereference follows an annotated tag with a "<name>^{}" line giving
	// the commit it points to, peeled however deep.
	out, err := runGit(dir, "show-ref", "--tags", "--dereference")
	if err != nil {
		return nil, nil
	}

	objects := map[string]string{}
	peeled := map[string]string{}

	for line := range strings.SplitSeq(out, "\n") {
		oid, ref, found := strings.Cut(line, " ")
		name, isTag := strings.CutPrefix(ref, "refs/tags/")

		if !found || !isTag {
			continue
		}

		if tagged, isPeeled := strings.CutSuffix(name, "^{}"); isPeeled {
			peeled[tagged] = oid
		} else {
			objects[name] = oid
		}
	}

	var (
		tags    []versionTag
		skipped []string
	)

	for _, name := range slices.Sorted(maps.Keys(objects)) {
		if !matchesTagGlobs(name) {
			continue
		}

		parsed, ok := tagVersion(name)
		if !ok {
			skipped = append(skipped, name)
			continue
		}

		commit, ok := peeled[name]
		if !ok {
			commit = objects[name]
		}

		tags = append(tags, versionTag{name: name, commit: commit, version: parsed})
	}

	return tags, skipped
}

// matchesTagGlobs reports whether one of tagGlobs matches name: whether it
// starts with a digit, or with a v and a digit.
func matchesTagGlobs(name string) bool {
	rest := strings.TrimPrefix(name, "v")

	return rest != "" && rest[0] >= '0' && rest[0] <= '9'
}

// tagVersion parses the name of a tag as a version. A name with a v is one
// when the rest is SemVer as go-version reads it, as it always was: v1.2.0,
// v1.2.0-rc.1, and v1.2 as 1.2.0. A name without a v has to be SemVer 2.0.0
// in full - 1.2.0, not 1.2 or 20261112 - since a bare number names a date or
// a build as often as a version. ok is false for any other name.
func tagVersion(name string) (*goversion.Version, bool) {
	if !matchesTagGlobs(name) {
		return nil, false
	}

	text, prefixed := strings.CutPrefix(name, "v")
	if !prefixed && !semVerRe.MatchString(text) {
		return nil, false
	}

	parsed, err := goversion.NewSemver(text)
	if err != nil {
		return nil, false
	}

	return parsed, true
}

// pickTag returns the tag the version is that of, given described, the tag
// git describe named: of the version tags on its commit, the one of the
// highest version, of the tags of one version the one with a v - v1.2.0 over
// 1.2.0 - and then the first by name. Which of the tags of a commit git
// describe names depends on how they were made, an annotated one over a
// lightweight one and the newest annotated one first, and the version must
// not. ok is false when tags has no described.
func pickTag(tags []versionTag, described string) (versionTag, bool) {
	idx := slices.IndexFunc(tags, func(tag versionTag) bool {
		return tag.name == described
	})
	if idx < 0 {
		return versionTag{name: "", commit: "", version: nil}, false
	}

	best := tags[idx]

	for _, tag := range tags {
		if tag.commit == best.commit && precedes(tag, best) {
			best = tag
		}
	}

	return best, true
}

// precedes reports whether tag takes precedence over other, a tag of the
// same commit (pickTag).
func precedes(tag, other versionTag) bool {
	cmp := tag.version.Compare(other.version)
	if cmp != 0 {
		return cmp > 0
	}

	prefixed, otherPrefixed := strings.HasPrefix(tag.name, "v"), strings.HasPrefix(other.name, "v")
	if prefixed != otherPrefixed {
		return prefixed
	}

	return tag.name < other.name
}

// deriveFallback builds the tagless 0.0.0-dev.<N>+g<sha> version of commit,
// the commit the version is of. N is the total commit count of its history,
// as git describe counts the commits since a tag in it: below the root, the
// commits elsewhere in the repository count as well, up to the last one that
// changed the directory, and none after it. Both N and the sha degrade to
// empty values when git is unavailable or there is no commit, yielding a
// still-valid 0.0.0-dev.0.
func deriveFallback(dir, commit string, dirty bool) Version {
	var ver Version

	count := "0"
	sha := ""

	if commit != "" {
		out, err := runGit(dir, "rev-list", "--count", commit)
		if err == nil {
			count = out
		}

		out, err = runGit(dir, "rev-parse", "--short", commit)
		if err == nil {
			sha = out
		}
	}

	ver.SemVer = "0.0.0-dev." + count + buildMetadata(gitSHA(sha), dirty)
	ver.Commit = sha
	ver.Dirty = dirty

	return ver
}

// renderDescribed applies rules 1-5 to a parsed tag, the number of commits since
// it, and the short sha.
func renderDescribed(parsed *goversion.Version, count int, sha string, dirty bool) string {
	core := coreString(parsed)
	pre := parsed.Prerelease()

	switch {
	case count == 0 && pre == "":
		// Exactly on a release tag: bare version (plus a dirty marker if any).
		return core + buildMetadata("", dirty)
	case count == 0:
		// Exactly on a pre-release tag.
		return core + "-" + pre + buildMetadata("", dirty)
	case pre == "":
		// Commits after a release tag bump the patch and open a -dev cycle.
		return bumpPatch(parsed) + "-dev." + strconv.Itoa(count) +
			buildMetadata(gitSHA(sha), dirty)
	default:
		// Commits after a pre-release tag extend the same pre-release segment;
		// the patch does not grow.
		return core + "-" + pre + ".dev." + strconv.Itoa(count) +
			buildMetadata(gitSHA(sha), dirty)
	}
}

// buildMetadata assembles the +<sha>.<dirty> build-metadata suffix, omitting the
// leading '+' entirely when there is nothing to attach.
func buildMetadata(sha string, dirty bool) string {
	var parts []string

	if sha != "" {
		parts = append(parts, sha)
	}

	if dirty {
		parts = append(parts, "dirty")
	}

	if len(parts) == 0 {
		return ""
	}

	return "+" + strings.Join(parts, ".")
}

// coreString renders the major.minor.patch core of a parsed version.
func coreString(ver *goversion.Version) string {
	s := ver.Segments()
	return fmt.Sprintf("%d.%d.%d", s[0], s[1], s[2])
}

// bumpPatch renders the core with the patch incremented by one.
func bumpPatch(ver *goversion.Version) string {
	s := ver.Segments()
	return fmt.Sprintf("%d.%d.%d", s[0], s[1], s[2]+1)
}

// splitDescribe pulls apart "<tag>-<N>-g<sha>". The tag may itself contain
// dashes (pre-release tags do), so the count and sha are taken from the right.
// The final bool is false when the input is not in describe --long shape.
func splitDescribe(desc string) (string, int, string, bool) {
	dash := strings.LastIndex(desc, "-")
	if dash < 0 {
		return "", 0, "", false
	}

	sha := desc[dash+1:]
	if !strings.HasPrefix(sha, "g") {
		return "", 0, "", false
	}

	rest := desc[:dash]

	dash = strings.LastIndex(rest, "-")
	if dash < 0 {
		return "", 0, "", false
	}

	count, err := strconv.Atoi(rest[dash+1:])
	if err != nil {
		return "", 0, "", false
	}

	return rest[:dash], count, sha, true
}

// gitSHA normalizes a short sha to the g-prefixed form used in build metadata
// (+gabc1234); an empty sha stays empty.
func gitSHA(sha string) string {
	if sha == "" {
		return ""
	}

	if strings.HasPrefix(sha, "g") {
		return sha
	}

	return "g" + sha
}

// shortSHA strips the g prefix that git describe puts in front of the sha.
func shortSHA(sha string) string {
	return strings.TrimPrefix(sha, "g")
}

// isDirty reports whether the working tree has uncommitted changes in
// pathspec, the directory of the project below the root of the repository and
// all of it at the root, ignoring the manifest lock so a regenerated lock
// alone does not mark a clean release.
func isDirty(dir string, pathspec []string) bool {
	// -z: no path is quoted, and the leading blank of the status of a change
	// not staged (" M app.manifest.lock") is not trimmed off with the output.
	out, err := gitOutput(dir, append([]string{"status", "--porcelain", "-z"}, pathspec...)...)
	if err != nil {
		return false
	}

	entries := strings.Split(out, "\x00")

	for idx := 0; idx < len(entries); idx++ {
		entry := entries[idx]
		if len(entry) <= porcelainPrefix {
			continue
		}

		// An entry is "XY <path>", a path relative to the root of the
		// repository; a rename or a copy is followed by the path it was of.
		paths := []string{entry[porcelainPrefix:]}

		if (entry[0] == 'R' || entry[0] == 'C') && idx+1 < len(entries) {
			idx++

			paths = append(paths, entries[idx])
		}

		for _, path := range paths {
			if filepath.Base(path) != lockFileName {
				return true
			}
		}
	}

	return false
}

// runGit runs git in dir and returns its trimmed stdout.
func runGit(dir string, args ...string) (string, error) {
	out, err := gitOutput(dir, args...)

	return strings.TrimSpace(out), err
}

// gitOutput runs git in dir and returns its stdout as it is.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}

	return string(out), nil
}
