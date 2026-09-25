package sdk

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ReleaseType is the stage of a Tarantool release.
type ReleaseType int

const (
	// ReleaseGA is a general-availability release, 3.2.1. It is the zero
	// value, so a TarantoolVersion given numbers alone is a release.
	ReleaseGA ReleaseType = iota
	// ReleaseNightly is the first commit of a release series, spelled
	// "entrypoint": 3.3.0-entrypoint.
	ReleaseNightly
	// ReleaseAlpha is an alpha: 3.0.0-alpha2.
	ReleaseAlpha
	// ReleaseBeta is a beta: 3.0.0-beta1.
	ReleaseBeta
	// ReleaseRC is a release candidate: 3.0.0-rc1.
	ReleaseRC
)

// String returns the release type as a version spells it: "entrypoint",
// "alpha", "beta" or "rc", and "" for ReleaseGA.
func (r ReleaseType) String() string {
	switch r {
	case ReleaseNightly:
		return "entrypoint"
	case ReleaseAlpha:
		return "alpha"
	case ReleaseBeta:
		return "beta"
	case ReleaseRC:
		return "rc"
	case ReleaseGA:
		return ""
	default:
		return "release(" + strconv.Itoa(int(r)) + ")"
	}
}

// rank orders release types from the earliest stage of a series to the
// release itself.
func (r ReleaseType) rank() int {
	switch r {
	case ReleaseNightly:
		return 0
	case ReleaseAlpha:
		return 1
	case ReleaseBeta:
		return 2 //nolint:mnd // Position in the release order.
	case ReleaseRC:
		return 3 //nolint:mnd // Position in the release order.
	default:
		return 4 //nolint:mnd // Position in the release order.
	}
}

// Edition is the edition of a Tarantool build.
type Edition string

const (
	// EditionUnknown is an edition that was not determined.
	EditionUnknown Edition = ""
	// EditionCE is Tarantool Community Edition.
	EditionCE Edition = "ce"
	// EditionEE is Tarantool Enterprise Edition.
	EditionEE Edition = "ee"
)

// TarantoolVersion is the version of a Tarantool build, as its --version
// reports it: 2.11.1-0-g96877bd-r579, say. It is a plain value; the zero
// value is 0.0.0.
type TarantoolVersion struct {
	// Major, Minor and Patch are the version numbers.
	Major, Minor, Patch uint64
	// Release is the release stage; ReleaseGA for a release.
	Release ReleaseType
	// ReleaseNum is the number of the stage, 2 in 3.0.0-rc2; 0 when the stage
	// has none.
	ReleaseNum uint64
	// Commits is the number of commits after the release tag, 12 in
	// 3.2.0-12-g1a2b3c4.
	Commits uint64
	// Hash is the abbreviated commit hash, 1a2b3c4 in 3.2.0-12-g1a2b3c4,
	// without the "g" of git describe; "" when the version has none.
	Hash string
	// Revision is the build revision of an Enterprise SDK, 579 in -r579.
	Revision uint64
	// BuildName is the name of a custom build that prefixes the version,
	// "debug" in debug-3.2.0; "" when there is none.
	BuildName string
	// Edition is the edition of the build; EditionUnknown when it was not
	// determined. The version string does not carry it.
	Edition Edition
	// Raw is the version string the version was parsed from; "" for a
	// version built from its fields.
	Raw string
}

// tarantoolVersionRe matches a Tarantool version string: an optional build
// name, the version numbers, then the optional release stage, commits,
// commit hash, revision and GC64 suffix.
var tarantoolVersionRe = regexp.MustCompile(`^(?:([^\d.+-].*)-)?` +
	`v?(\d+)\.(\d+)\.(\d+)` +
	`(?:-(entrypoint|rc|alpha|beta)(\d+)?)?` +
	`(?:-(\d+))?` +
	`(?:-g([a-f0-9]+))?(?:-r(\d+))?(?:-gc64|-nogc64)?$`)

// Submatch indexes of tarantoolVersionRe.
const (
	reBuildName = iota + 1
	reMajor
	reMinor
	rePatch
	reRelease
	reReleaseNum
	reCommits
	reHash
	reRevision
)

// errInvalidVersion reports a string that is not a Tarantool version.
var errInvalidVersion = errors.New("invalid Tarantool version")

// ParseTarantoolVersion parses a Tarantool version string - the last word of
// the first line of tarantool --version. The edition is not part of the
// string and stays EditionUnknown.
func ParseTarantoolVersion(str string) (TarantoolVersion, error) {
	match := tarantoolVersionRe.FindStringSubmatch(str)
	if match == nil {
		return TarantoolVersion{}, fmt.Errorf("%w %q", errInvalidVersion, str)
	}

	version := TarantoolVersion{
		Major:      0,
		Minor:      0,
		Patch:      0,
		Release:    ReleaseGA,
		ReleaseNum: 0,
		Commits:    0,
		Hash:       match[reHash],
		Revision:   0,
		BuildName:  match[reBuildName],
		Edition:    EditionUnknown,
		Raw:        str,
	}

	numbers := []struct {
		dst *uint64
		src string
	}{
		{&version.Major, match[reMajor]},
		{&version.Minor, match[reMinor]},
		{&version.Patch, match[rePatch]},
		{&version.ReleaseNum, match[reReleaseNum]},
		{&version.Commits, match[reCommits]},
		{&version.Revision, match[reRevision]},
	}
	for _, number := range numbers {
		if number.src == "" {
			continue
		}

		value, err := strconv.ParseUint(number.src, 10, 64)
		if err != nil {
			return TarantoolVersion{}, fmt.Errorf("%w %q: %w", errInvalidVersion, str, err)
		}

		*number.dst = value
	}

	switch match[reRelease] {
	case "entrypoint":
		version.Release = ReleaseNightly
	case "alpha":
		version.Release = ReleaseAlpha
	case "beta":
		version.Release = ReleaseBeta
	case "rc":
		version.Release = ReleaseRC
	}

	return version, nil
}

// String returns Raw when the version was parsed, and otherwise spells the
// version from its fields the way Tarantool does:
// [build-]major.minor.patch[-stageN][-commits][-ghash][-rrevision].
func (v TarantoolVersion) String() string {
	if v.Raw != "" {
		return v.Raw
	}

	var out strings.Builder

	if v.BuildName != "" {
		out.WriteString(v.BuildName + "-")
	}

	fmt.Fprintf(&out, "%d.%d.%d", v.Major, v.Minor, v.Patch)

	if v.Release != ReleaseGA {
		out.WriteString("-" + v.Release.String())

		if v.ReleaseNum != 0 {
			out.WriteString(strconv.FormatUint(v.ReleaseNum, 10))
		}
	}

	if v.Commits != 0 || v.Hash != "" {
		out.WriteString("-" + strconv.FormatUint(v.Commits, 10))
	}

	if v.Hash != "" {
		out.WriteString("-g" + v.Hash)
	}

	if v.Revision != 0 {
		out.WriteString("-r" + strconv.FormatUint(v.Revision, 10))
	}

	return out.String()
}

// Compare returns -1 when v is older than other, 1 when it is newer and 0
// when neither is. Versions are ordered by their numbers, then by release
// stage (entrypoint, alpha, beta, rc, release) and its number, then by
// commits and revision. The hash, build name, edition and Raw do not take
// part.
func (v TarantoolVersion) Compare(other TarantoolVersion) int {
	return cmp.Or(
		cmp.Compare(v.Major, other.Major),
		cmp.Compare(v.Minor, other.Minor),
		cmp.Compare(v.Patch, other.Patch),
		cmp.Compare(v.Release.rank(), other.Release.rank()),
		cmp.Compare(v.ReleaseNum, other.ReleaseNum),
		cmp.Compare(v.Commits, other.Commits),
		cmp.Compare(v.Revision, other.Revision),
	)
}

// AtLeast reports whether v is major.minor.patch or newer. A pre-release of
// that version is older: 3.0.0-rc1 is not at least 3.0.0.
func (v TarantoolVersion) AtLeast(major, minor, patch uint64) bool {
	release := TarantoolVersion{
		Major:      major,
		Minor:      minor,
		Patch:      patch,
		Release:    ReleaseGA,
		ReleaseNum: 0,
		Commits:    0,
		Hash:       "",
		Revision:   0,
		BuildName:  "",
		Edition:    EditionUnknown,
		Raw:        "",
	}

	return v.Compare(release) >= 0
}
