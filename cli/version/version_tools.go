package version

import (
	"fmt"
	"regexp"
	"strings"

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
	str  string
}

// newRelease create new Release base on passed data.
func newRelease(release string, releaseNum string) (Release, error) {
	newRelease := Release{Type: TypeRelease, str: release}
	if release != "" {
		switch release {
		case "rc":
			newRelease.Type = TypeRC
		case "alpha":
			newRelease.Type = TypeAlpha
		case "beta":
			newRelease.Type = TypeBeta
		case "entrypoint":
			newRelease.Type = TypeNightly
		default:
			return newRelease, fmt.Errorf("unknown release type %q", release)
		}
		if releaseNum != "" {
			var err error
			if newRelease.Num, err = util.AtoiUint64(releaseNum); err != nil {
				return newRelease, fmt.Errorf("bad release number format %q: %w", releaseNum, err)
			}
			newRelease.str += releaseNum
		}
	}
	return newRelease, nil
}

func (release Release) String() string {
	return release.str
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

func createVersionRegexp(isStrict bool) *regexp.Regexp {
	// Part 1 (optional) -> custom build name, started with letter (not digit)
	// Part 2 (optional) -> gc suffix,
	// Part 3            -> major.minor.patch version number,
	// Part 4 (optional) -> release type and number (e.g: rc2),
	// Part 5 (optional) -> additional commits,
	// Part 6 (optional) -> commit hash and revision.
	// GC suffix is not saved, since it is not part of version.
	matchString := `^((?P<buildname>[^\d\.\+-].*)-)?` +
		`v?(?:(?P<major>\d+)){1}(?:\.(?P<minor>\d+)){1}(?:\.(?P<patch>\d+)){1}` +
		`(?:-(?P<release>entrypoint|rc|alpha|beta)(?P<releaseNum>\d+)?)?` +
		`(?:-(?P<additional>\d+))?` +
		`(?:-(?P<hash>g[a-f0-9]+))?(?:-r(?P<revision>\d+))?(-gc64|-nogc64)?$`
	if !isStrict {
		matchString = strings.Replace(matchString, "{1}", "?", 3)
	}
	return regexp.MustCompile(matchString)
}

func matchVersionParts(version string, isStrict bool) (map[string]string, error) {
	re := createVersionRegexp(isStrict)
	matches := util.FindNamedMatches(re, version)
	if len(matches) == 0 {
		return nil, fmt.Errorf("failed to parse version %q: format is not valid", version)
	}
	return matches, nil
}

// Parse parses a version string and return the version value it represents.
func Parse(verStr string) (Version, error) {
	version := Version{}
	matches, err := matchVersionParts(verStr, true)
	if err != nil {
		return version, err
	}

	if matches["buildname"] != "" {
		version.BuildName = matches["buildname"]
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

	if version.Release, err = newRelease(matches["release"], matches["releaseNum"]); err != nil {
		return version, err
	}

	version.Hash = matches["hash"]

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

// ParseTt parses a tt version string with format '<major>.<minor>.<patch>.<hash>'
// and return the version value it represents.
func ParseTt(verStr string) (Version, error) {
	verToParse := strings.Trim(verStr, "\n")
	sepIndex := strings.LastIndex(verToParse, ".")
	if sepIndex == -1 {
		return Version{}, fmt.Errorf("failed to parse version %q: format is not valid", verStr)
	}

	verStr = verToParse[:sepIndex]
	numVersions := strings.Split(verStr, ".")
	if len(numVersions) != 3 {
		return Version{}, fmt.Errorf("the version of %q does not match"+
			" <major>.<minor>.<patch> format", verStr)
	}

	var err error
	ttVersion := Version{}

	ttVersion.Major, err = util.AtoiUint64(numVersions[0])
	if err != nil {
		return Version{}, err
	}
	ttVersion.Minor, err = util.AtoiUint64(numVersions[1])
	if err != nil {
		return Version{}, err
	}
	ttVersion.Patch, err = util.AtoiUint64(numVersions[2])
	if err != nil {
		return Version{}, err
	}

	hashStr := verToParse[sepIndex+1:]
	isHashValid, err := util.IsValidCommitHash(hashStr)
	if err != nil {
		return Version{}, err
	}
	if !isHashValid {
		return Version{}, fmt.Errorf("hash %q has a wrong format", hashStr)
	}
	ttVersion.Hash = hashStr
	ttVersion.Str = verToParse

	return ttVersion, nil
}

// VersionSlice attaches the methods of sort.Interface to []Version, sorting from oldest to newest.
type VersionSlice []Version

// sort.Interface Len implementation
func (v VersionSlice) Len() int {
	return len(v)
}

// IsLess returns true if verLeft is less than verRight.
func IsLess(verLeft Version, verRight Version) bool {
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

// sort.Interface Less implementation, sorts from oldest to newest
func (v VersionSlice) Less(i, j int) bool {
	return IsLess(v[i], v[j])
}

// sort.Interface Swap implementation
func (v VersionSlice) Swap(i, j int) {
	v[i], v[j] = v[j], v[i]
}
