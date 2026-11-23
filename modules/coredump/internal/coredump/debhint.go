package coredump

import (
	"debug/elf"
	"fmt"
	"regexp"
	"strings"
)

// staticDebVersion matches the version of a static build of Tarantool
// Community, as tarantool --version prints it: a release of series 3, with
// no pre-release suffix, built at its tag, git describe's count 0, and
// nothing after the commit, no -dirty. RWS has the static builds of series
// 3 alone, and an Enterprise build, a pre-release, a build of a commit past
// the tag or of a dirty tree is none of them.
var staticDebVersion = regexp.MustCompile(`^Tarantool (3\.\d+\.\d+)-0-g[0-9a-f]{7,40}$`)

// staticDebRelease returns the release of the static deb whose tarantool
// may be the tarantool of a core: of the version line, the first line of
// tarantool --version, of a static build of Community (see
// staticDebVersion), by an executable of type typ, which the static builds
// are linked as, ET_EXEC, with the GNU build ID build, which they carry.
// It reports false of any other: a PIE a user or a distribution builds has
// another build ID, and its debug file is elsewhere.
func staticDebRelease(line string, typ elf.Type, build string) (string, bool) {
	match := staticDebVersion.FindStringSubmatch(strings.TrimSpace(line))
	if match == nil || typ != elf.ET_EXEC || build == "" {
		return "", false
	}

	return match[1], true
}

// staticDebHint returns the hint of the tarantool of the static deb of the
// release release, which has the DWARF the stripped static builds of the
// release lack, for a core of the build ID build: the static packages of
// Tarantool Community, RPM or deb, are the same builds, of the same build
// ID, the deb alone keeps the DWARF of the executable, and tt binary
// install takes it from there. action says what to do with it; the build
// ID is given for the user to compare.
func staticDebHint(release, build, action string) string {
	return fmt.Sprintf("the tarantool of the static deb of Tarantool %s, which `tt binary "+
		"install tarantool %s` installs, has the debugging information: if its build ID is that "+
		"of the core, %s, %s", release, release, build, action)
}
