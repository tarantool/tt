package coredump

import (
	"bufio"
	"os"
	"regexp"
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
