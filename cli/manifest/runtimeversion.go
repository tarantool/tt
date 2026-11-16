package manifest

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	goversion "github.com/hashicorp/go-version"
)

// This file is the rule a runtime version - of the tarantool, the tt or the
// TCM a package is packed with, or installed next to - meets a [platform]
// constraint by. tt package pack picks the runtime to bundle by it, and tt
// package install checks the runtime an archive bundles against the
// [platform] of the project by it, so that an archive pack made is one
// install takes.

var (
	// ErrRuntimeConstraint reports a [platform] constraint that is not a
	// range tt can read.
	ErrRuntimeConstraint = errors.New("invalid [platform] version constraint")
	// ErrRuntimeVersion reports a runtime version that is not a version.
	ErrRuntimeVersion = errors.New("not a runtime version")
)

// releaseBuild matches the version a Community tarantool built from a release
// tag reports: the release, zero commits after the tag and the commit hash.
var releaseBuild = regexp.MustCompile(`^(\d+\.\d+\.\d+)-0-g[0-9a-f]+$`)

// RuntimeSatisfies reports whether the runtime version ver meets spec, the
// version part of a [platform] constraint: ">=3.0.0,<4.0.0", say. An empty
// spec is met by every version, and an empty version meets none, not even an
// empty spec.
//
// The version is compared by its major.minor.patch core (RuntimeCoreVersion),
// so a prerelease meets a range its release would meet: 3.0.0-alpha.2 meets
// ">=3.0.0". This is deliberately looser than semver's own rule, which keeps
// a prerelease out of a range that does not itself name one. That rule exists
// for resolving dependencies, where an unreleased version is a hazard. A
// runtime version describes a binary the user already has, and every
// development build of Tarantool reports one, "3.8.0-entrypoint-49-g97a3b38040":
// under the strict rule such a build meets no ordinary constraint at all, and
// neither does a prerelease of tt. Whoever takes a prerelease by this rule
// says so (IsRuntimePrerelease).
//
// A spec tt cannot read is ErrRuntimeConstraint, and a version that is no
// version ErrRuntimeVersion.
func RuntimeSatisfies(ver, spec string) (bool, error) {
	if strings.TrimSpace(ver) == "" {
		return false, nil
	}

	spec = strings.TrimSpace(spec)
	if spec == "" {
		return true, nil
	}

	constraints, err := goversion.NewConstraint(spec)
	if err != nil {
		return false, fmt.Errorf("%w: %q (expected a comma-separated range like %q)",
			ErrRuntimeConstraint, spec, ">=3.0.0,<4.0.0")
	}

	parsed, err := goversion.NewVersion(RuntimeCoreVersion(ver))
	if err != nil {
		return false, fmt.Errorf("%w: %q", ErrRuntimeVersion, ver)
	}

	return constraints.Check(parsed), nil
}

// RuntimeCoreVersion reduces a runtime version to its major.minor.patch
// core, dropping a leading v and any prerelease or build-metadata suffix:
// 3.8.0 of "v3.8.0-rc1+deadbeef".
func RuntimeCoreVersion(ver string) string {
	ver = normalizeRuntimeVersion(ver)

	// Cut build metadata first, then the prerelease: "3.8.0-rc1+deadbeef".
	ver, _, _ = strings.Cut(ver, "+")
	ver, _, _ = strings.Cut(ver, "-")

	return ver
}

// IsRuntimePrerelease reports whether a runtime version carries a prerelease
// or build-metadata suffix, i.e. whether RuntimeSatisfies compared it by a
// core it is not.
//
// A build of a release tag, 3.8.1-0-ge643f88a5e0c, is the release itself
// (RuntimeRelease): the zero commits after the tag and the commit git
// describe adds make no prerelease of it. A build that is commits past its
// tag, or one of an -rc or -alpha tag, is still one.
func IsRuntimePrerelease(ver string) bool {
	ver = normalizeRuntimeVersion(ver)
	if releaseBuild.MatchString(ver) {
		return false
	}

	return RuntimeCoreVersion(ver) != ver
}

// RuntimeRelease returns the release a Community tarantool built from a
// release tag reports itself as a build of: 3.8.1 of 3.8.1-0-ge643f88a5e0c.
// ok is false for any other version.
func RuntimeRelease(ver string) (string, bool) {
	match := releaseBuild.FindStringSubmatch(normalizeRuntimeVersion(ver))
	if match == nil {
		return "", false
	}

	return match[1], true
}

// normalizeRuntimeVersion strips a leading "v" and surrounding whitespace,
// leaving the version otherwise intact.
func normalizeRuntimeVersion(ver string) string {
	return strings.TrimPrefix(strings.TrimSpace(ver), "v")
}
