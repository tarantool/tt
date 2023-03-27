package version

import (
	"fmt"
	"regexp"

	"github.com/tarantool/tt/cli/util"
)

type ReleaseType uint16

const (
	TypeNightly ReleaseType = iota
	TypeAlpha
	TypeBeta
	TypeRC
	TypeRelease

	// CliSeparator is used in commands to specify version. E.g: program=version.
	CliSeparator = "="
	// FsSeparator is used in file names to specify version. E.g: program_version.
	FsSeparator = "_"
)

type Release struct {
	Type ReleaseType
	Num  uint64
}

type Version struct {
	Major      uint64  // Major
	Minor      uint64  // Minor
	Patch      uint64  // Patch
	Additional uint64  // Additional commits.
	Revision   uint64  // Revision number.
	Release    Release // Release type.
	Hash       string  // Commit hash.
	Str        string  // String representation.
	Tarball    string  // Tarball name.
	BuildName  string  // Custom build name.
}

// Parse parses a version string and return the version value it represents.
func Parse(verStr string) (Version, error) {
	version := Version{}
	var err error

	// Part 1 (optional) -> custom build name,
	// Part 2 (optional) -> gc suffix,
	// Part 3            -> major.minor.patch version number,
	// Part 4 (optional) -> release type and number (e.g: rc2),
	// Part 5 (optional) -> additional commits,
	// Part 6 (optional) -> commit hash and revision.
	re := regexp.MustCompile(
		`^((?P<buildname>.+)-)?` +
			`v?(?P<major>\d+)\.(?P<minor>\d+)\.(?P<patch>\d+)` +
			`(?:-(?P<release>entrypoint|rc|alpha|beta)(?P<releaseNum>\d+)?)?` +
			`(?:-(?P<additional>\d+))?` +
			`(?:-(?P<hash>g[a-f0-9]+))?(?:-r(?P<revision>\d+))?(?:-nogc64)?$`)

	matches := util.FindNamedMatches(re, verStr)
	if len(matches) == 0 {
		return version, fmt.Errorf("failed to parse version %q: format is not valid", verStr)
	}

	if matches["buildname"] != "" {
		version.BuildName = matches["buildname"]
	}

	if matches["release"] != "" {
		switch matches["release"] {
		case "rc":
			version.Release.Type = TypeRC
		case "alpha":
			version.Release.Type = TypeAlpha
		case "beta":
			version.Release.Type = TypeBeta
		case "entrypoint":
			version.Release.Type = TypeNightly
		}
	} else {
		version.Release.Type = TypeRelease
	}

	if version.Major, err = util.AtoiUint64(matches["major"]); err != nil {
		return version, err
	}

	if version.Minor, err = util.AtoiUint64(matches["minor"]); err != nil {
		return version, err
	}

	if version.Patch, err = util.AtoiUint64(matches["patch"]); err != nil {
		return version, err
	}

	version.Hash = matches["hash"]

	if matches["releaseNum"] != "" {
		if version.Release.Num, err = util.AtoiUint64(matches["releaseNum"]); err != nil {
			return version, err
		}
	}

	if matches["additional"] != "" {
		if version.Additional, err = util.AtoiUint64(matches["additional"]); err != nil {
			return version, err
		}
	}

	if matches["revision"] != "" {
		if version.Revision, err = util.AtoiUint64(matches["revision"]); err != nil {
			return version, err
		}
	}

	version.Str = verStr

	return version, nil
}

// VersionSlice attaches the methods of sort.Interface to []Version, sorting from oldest to newest.
type VersionSlice []Version

// sort.Interface Len implementation
func (v VersionSlice) Len() int {
	return len(v)
}

// sort.Interface Less implementation, sorts from oldest to newest
func (v VersionSlice) Less(i, j int) bool {
	verLeft := v[i]
	verRight := v[j]

	left := []uint64{verLeft.Major, verLeft.Minor,
		verLeft.Patch, uint64(verLeft.Release.Type),
		verLeft.Release.Num, verLeft.Additional, verLeft.Revision}
	right := []uint64{verRight.Major, verRight.Minor,
		verRight.Patch, uint64(verRight.Release.Type),
		verRight.Release.Num, verRight.Additional, verRight.Revision}

	largestLen := util.Max(len(left), len(right))

	for i := 0; i < largestLen; i++ {
		var valLeft, valRight uint64 = 0, 0
		if i < len(left) {
			valLeft = left[i]
		}

		if i < len(right) {
			valRight = right[i]
		}

		if valLeft != valRight {
			return valLeft < valRight
		}
	}

	return false
}

// sort.Interface Swap implementation
func (v VersionSlice) Swap(i, j int) {
	v[i], v[j] = v[j], v[i]
}
