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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	goversion "github.com/hashicorp/go-version"
)

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
	Commit string // Short sha "abc1234", or empty when there is no commit.
	Dirty  bool   // Working tree had uncommitted changes (lock excluded).
	Flavor string // ce|ee - a build fact, set by the build, not by Derive.
}

// Derive determines the package version from the git tree rooted at dir and a
// VERSION file in dir, if present.
//
// Source precedence:
//  1. a VERSION file next to the manifest (one line, SemVer 2.0.0);
//  2. the most recent v* tag reachable from HEAD (git describe);
//  3. a tagless fallback of 0.0.0-dev.<N>+g<sha>, N = commits in HEAD's history.
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

	dirty := isDirty(dir)

	ver, ok = deriveFromGitDescribe(dir, dirty)
	if ok {
		return ver, nil
	}

	return deriveFallback(dir, dirty), nil
}

// deriveFromFile reads the VERSION file. The file is an explicit pin: the
// version is taken verbatim (minus a leading "v"), without git metadata or a
// dirty marker. ok is false when the file is absent.
func deriveFromFile(dir string) (Version, bool, error) {
	var ver Version

	//nolint:gosec // Reads the caller's own manifest directory, not user input.
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

// deriveFromGitDescribe runs git describe and applies the pre-release / dev
// cycle rules. ok is false when there is no usable SemVer v* tag, in which case
// the caller falls back to deriveFallback.
func deriveFromGitDescribe(dir string, dirty bool) (Version, bool) {
	var ver Version

	// The --match 'v[0-9]*' pattern requires a digit after the v, so vfinal and
	// friends never reach the parser; --long always prints the <tag>-<N>-g<sha> form.
	out, err := runGit(dir, "describe", "--tags", "--long", "--match", "v[0-9]*", "HEAD")
	if err != nil {
		return ver, false
	}

	tag, count, sha, ok := splitDescribe(out)
	if !ok {
		return ver, false
	}

	parsed, err := goversion.NewSemver(strings.TrimPrefix(tag, "v"))
	if err != nil {
		// A v* tag that is not SemVer (e.g. vfinal) is not a version: skip it.
		return ver, false
	}

	ver.SemVer = renderDescribed(parsed, count, sha, dirty)
	ver.Commit = shortSHA(sha)
	ver.Dirty = dirty

	return ver, true
}

// deriveFallback builds the tagless 0.0.0-dev.<N>+g<sha> version. N is the total
// commit count of HEAD; both N and the sha degrade to empty values when git is
// unavailable, yielding a still-valid 0.0.0-dev.0.
func deriveFallback(dir string, dirty bool) Version {
	var ver Version

	count := "0"

	out, err := runGit(dir, "rev-list", "--count", "HEAD")
	if err == nil {
		count = out
	}

	sha := ""

	out, err = runGit(dir, "rev-parse", "--short", "HEAD")
	if err == nil {
		sha = out
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

// isDirty reports whether the working tree has uncommitted changes, ignoring
// the manifest lock so a regenerated lock alone does not mark a clean release.
func isDirty(dir string) bool {
	out, err := runGit(dir, "status", "--porcelain")
	if err != nil {
		return false
	}

	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		// Porcelain lines are "XY <path>"; drop the two status columns.
		path := strings.TrimSpace(line[min(porcelainPrefix, len(line)):])
		if filepath.Base(path) == lockFileName {
			continue
		}

		return true
	}

	return false
}

// runGit runs git in dir and returns its trimmed stdout.
func runGit(dir string, args ...string) (string, error) {
	//nolint:gosec // Fixed program (git) with internally built args on the caller's dir.
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}

	return strings.TrimSpace(string(out)), nil
}
