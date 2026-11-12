package rocks

import (
	"regexp"
	"strings"

	luarocks "github.com/tarantool/go-luarocks"
)

// The rock versions of the versions tt derives for a package (version.Derive)
// once rocktree.RockVersion has turned them into rock versions: the core and
// any pre-release, then what git adds - dev, the commits since the tag and
// the short sha after a g - and dirty for uncommitted changes, then the
// revision, which a constraint leaves out.
var (
	// commitVersionRe is the version of a commit: 1.2.1.dev.3.gabc1234-1,
	// 2.0.0.rc.1.dev.3.gabc1234-1, 0.0.0.dev.36.g7170ebf-1.
	commitVersionRe = regexp.MustCompile(
		`^[0-9]+\.[0-9]+\.[0-9]+(\.[0-9A-Za-z]+)*\.dev\.[0-9]+\.g[0-9a-f]{4,64}(-[0-9]+)?$`)
	// uncommittedVersionRe is the version of a tree with uncommitted changes:
	// 1.2.0.dirty-1, 0.0.0.dev.36.g7170ebf.dirty-1.
	uncommittedVersionRe = regexp.MustCompile(
		`^[0-9]+\.[0-9]+\.[0-9]+(\.[0-9A-Za-z]+)*\.dirty(-[0-9]+)?$`)
)

// Moving reports whether a version may name different code on a later day,
// rather than naming a release: a branch, a version with scm or dev in it as
// LuaRocks reads it, or a version tt made from uncommitted changes
// (Uncommitted).
//
// The version tt makes from a commit, 0.0.0.dev.36.g7170ebf-1, has a dev in it
// too, but it names one commit, so it is a release as far as tt is concerned.
// One with no commit in it stays moving: scm-1, dev-1, and the 0.0.0.dev.0-1
// tt makes out of git.
func Moving(version luarocks.Version) bool {
	if Uncommitted(version) {
		return true
	}

	if !version.IsSCM && !version.IsDev {
		return false
	}

	return !commitVersionRe.MatchString(strings.TrimSpace(version.Raw))
}

// Uncommitted reports whether a version is one tt made from a tree with
// uncommitted changes, which ends in dirty: every such build of a commit has
// the same version, whatever its changes were.
func Uncommitted(version luarocks.Version) bool {
	return uncommittedVersionRe.MatchString(strings.TrimSpace(version.Raw))
}
