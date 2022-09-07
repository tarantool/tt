package version

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/tarantool/tt/cli/util"
)

type ReleaseType uint16

const (
	TypeNightly ReleaseType = iota
	TypeAlpha
	TypeBeta
	TypeRC
	TypeRelease
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
}

// GetVersionDetails collects information about all version details.
func GetVersionDetails(verStr string) (Version, error) {
	version := Version{}
	var err error

	// Part 1            -> major.minor.patch version number,
	// Part 2 (optional) -> release type and number (e.g: rc2),
	// Part 3 (optional) -> additional commits,
	// Part 4 (optional) -> commit hash and revision.
	re := regexp.MustCompile(
		`^(?P<major>\d+)\.(?P<minor>\d+)\.(?P<patch>\d+)` +
			`(?:-(?P<release>entrypoint|rc|alpha|beta)(?P<releaseNum>\d+)?)?` +
			`(?:-(?P<additional>\d+))?` +
			`(?:-(?P<hash>g[a-f0-9]+))?(?:-r(?P<revision>\d+))?$`)

	matches := util.FindNamedMatches(re, verStr)
	if len(matches) == 0 {
		return version, fmt.Errorf("Failed to parse version: format is not valid")
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

	return version, nil
}

// SortVersions sorts versions from oldest to newest.
func SortVersions(versions []Version) {
	sort.SliceStable(versions, func(i, j int) bool {
		verLeft := versions[i]
		verRight := versions[j]

		left := []uint64{verLeft.Major, verLeft.Minor,
			verLeft.Patch, uint64(verLeft.Release.Type),
			verLeft.Release.Num, verLeft.Additional, verLeft.Revision}
		right := []uint64{verRight.Major, verRight.Minor,
			verRight.Patch, uint64(verRight.Release.Type),
			verRight.Release.Num, verRight.Additional, verRight.Revision}

		lagerLen := util.Max(len(left), len(right))

		for i := 0; i < lagerLen; i++ {
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
	})
}
