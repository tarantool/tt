package coredump

import (
	"bufio"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// tarantoolVersion is what the version file of an archive, the output of
// tarantool --version, tells of the Tarantool that dumped the core.
type tarantoolVersion struct {
	// Line is the first line of the file, the version as Tarantool prints
	// it: "Tarantool 3.9.0-0-g8b9a0f59a95b".
	Line string
	// Enterprise reports Tarantool Enterprise, "Tarantool Enterprise 3.7.0-0-g...".
	Enterprise bool
	// Tag is the tag of the release in tarantool/tarantool: 3.9.0, or with
	// the suffix of a pre-release, 3.0.0-rc1 or 3.10.0-entrypoint.
	Tag string
	// Release is the version alone, x.y.z.
	Release string
	// Commit is the commit of the build, abbreviated, as git describe
	// gives it after -g; empty when the line has none.
	Commit string
}

// versionPattern matches the first line of tarantool --version: the
// edition, the tag of the release, which a pre-release suffix such as
// -rc1 or -entrypoint ends, and git describe's count of commits and the
// abbreviated commit. What follows, such as -dirty or the -r<build> of
// Enterprise, is ignored.
var versionPattern = regexp.MustCompile(
	`^Tarantool (Enterprise )?((\d+\.\d+\.\d+)(?:-[a-z]+\d*)?)(?:-\d+-g([0-9a-f]{7,40}))?\b`)

// parseVersion reads the first line of tarantool --version. It reports
// false for a line that is no version of Tarantool.
func parseVersion(line string) (tarantoolVersion, bool) {
	line = strings.TrimSpace(line)

	match := versionPattern.FindStringSubmatch(line)
	if match == nil {
		return tarantoolVersion{Line: line, Enterprise: false, Tag: "", Release: "", Commit: ""},
			false
	}

	return tarantoolVersion{
		Line:       line,
		Enterprise: match[1] != "",
		Tag:        match[2],
		Release:    match[3],
		Commit:     match[4],
	}, true
}

// releasePattern matches a release of Tarantool alone, x.y.z: the Release
// of a version, and the ends of the range of the versions in a manifest.
var releasePattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

// release is a release of Tarantool, x.y.z, by its numbers.
type release [3]int

// parseRelease reads a release, x.y.z. It reports false for anything else,
// a pre-release suffix too.
func parseRelease(text string) (release, bool) {
	match := releasePattern.FindStringSubmatch(text)
	if match == nil {
		return release{}, false
	}

	var parsed release

	for part := range parsed {
		number, err := strconv.Atoi(match[part+1])
		if err != nil {
			return release{}, false
		}

		parsed[part] = number
	}

	return parsed, true
}

// compare returns a negative number, zero or a positive one as r is before
// other, the same or after it.
func (r release) compare(other release) int {
	return slices.Compare(r[:], other[:])
}

// minor returns the minor of r, x.y, as its first patch, x.y.0.
func (r release) minor() release {
	return release{r[0], r[1], 0}
}

// minorRange is a versionRange by the minors of its ends, x.y.0 each. CI
// runs an extension on the last patch of every minor from the one of the
// first end of the range of its manifest to the one of the last: a core of
// any patch of those minors is in the range, one of another minor is not.
// 3.9.1 is in 3.0.2..3.9.0, and so is 3.3.0 in 3.3.5..3.9.0.
type minorRange struct {
	// first is the minor of the first end.
	first release
	// last is the minor of the last end.
	last release
}

// minors returns r by the minors of its ends. It reports false when an end
// is no release x.y.z: a core cannot be told to be in such a range or out
// of it.
func (r versionRange) minors() (minorRange, bool) {
	first, ok := parseRelease(r.Min)
	if !ok {
		return minorRange{first: release{}, last: release{}}, false
	}

	last, ok := parseRelease(r.Max)
	if !ok {
		return minorRange{first: release{}, last: release{}}, false
	}

	return minorRange{first: first.minor(), last: last.minor()}, true
}

// String returns m as a warning names it, by its minors: "3.3 to 3.9", or
// "3.9" for a range of one minor.
func (m minorRange) String() string {
	first := strconv.Itoa(m.first[0]) + "." + strconv.Itoa(m.first[1])
	if m.first == m.last {
		return first
	}

	return first + " to " + strconv.Itoa(m.last[0]) + "." + strconv.Itoa(m.last[1])
}

// excludes reports whether the release version, x.y.z, is out of m: of a
// minor before the first or after the last. A version that is no release
// is excluded by nothing: nothing can be told of it.
func (m minorRange) excludes(version string) bool {
	parsed, ok := parseRelease(version)
	if !ok {
		return false
	}

	minor := parsed.minor()

	return minor.compare(m.first) < 0 || minor.compare(m.last) > 0
}

// readVersion returns the first line of the version file at path.
func readVersion(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err //nolint:wrapcheck // The *PathError names the file.
	}

	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		return scanner.Text(), nil
	}

	return "", scanner.Err() //nolint:wrapcheck // A read error of the file names it.
}

// refs returns the refs of tarantool/tarantool the files of the version
// are looked up at, in order: the commit of a Community build, then the
// tag of its release, which a commit tarantool/tarantool does not have,
// one of a local build, falls back to. Enterprise is built from a
// repository of its own, whose commits tarantool/tarantool does not have:
// its files are looked up at the tag of the Community release of its
// version.
func (v tarantoolVersion) refs() []string {
	if v.Enterprise {
		return []string{v.Release}
	}

	if v.Commit == "" {
		return []string{v.Tag}
	}

	return []string{v.Commit, v.Tag}
}
