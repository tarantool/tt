package version

import (
	"fmt"

	"github.com/tarantool/tt/cli/util"
)

type (
	requiredField  uint8
	requiredFields []requiredField
)

const (
	// Values according order of Fields in regex from `matchVersionParts()`.
	requiredFieldBuildName requiredField = iota
	requiredFieldMajor
	requiredFieldMinor
	requiredFieldPatch
	requiredFieldReleaseType
	requiredFieldReleaseNum
	requiredFieldAdditional
	requiredFieldHash
	requiredFieldRevision

	countOfRequiredFields
)

// NotFoundError is sentinel error for case when required version not found.
type NotFoundError struct {
	expected string
}

// Error implements the [error] interface.
func (e NotFoundError) Error() string {
	return fmt.Sprintf("not matched with: %q", e.expected)
}

// exploreMatchVersion examines the given string and retrieves the version components,
// that define the rules for checking for version consistency.
func exploreMatchVersion(verStr string) (Version, requiredFields, error) {
	var version Version
	fields := make(requiredFields, 0, countOfRequiredFields)

	matches, err := matchVersionParts(verStr, false)
	if err != nil {
		return version, fields, err
	}

	version.BuildName = matches["buildname"]
	if version.BuildName != "" {
		fields = append(fields, requiredFieldBuildName)
	}

	if matches["major"] != "" {
		if version.Major, err = util.AtoiUint64(matches["major"]); err != nil {
			return version, fields, fmt.Errorf("can't parse Major: %w", err)
		}
		fields = append(fields, requiredFieldMajor)
	}

	if matches["minor"] != "" {
		if matches["major"] == "" {
			return version, fields, fmt.Errorf("minor version requires major to be specified")
		}
		if version.Minor, err = util.AtoiUint64(matches["minor"]); err != nil {
			return version, fields, fmt.Errorf("can't parse Minor: %w", err)
		}
		fields = append(fields, requiredFieldMinor)
	}

	if matches["patch"] != "" {
		if matches["minor"] == "" {
			return version, fields, fmt.Errorf("patch version requires minor to be specified")
		}
		if version.Patch, err = util.AtoiUint64(matches["patch"]); err != nil {
			return version, fields, fmt.Errorf("can't parse Patch version: %w", err)
		}
		fields = append(fields, requiredFieldPatch)
	}

	if matches["release"] != "" {
		version.Release, err = newRelease(matches["release"], matches["releaseNum"])
		if err != nil {
			return version, fields, fmt.Errorf("can't parse Release: %w", err)
		}
		fields = append(fields, requiredFieldReleaseType)
		if matches["releaseNum"] != "" {
			fields = append(fields, requiredFieldReleaseNum)
		}
	} else if len(fields) > 0 {
		// By default require 'release' version.
		version.Release.Type = TypeRelease
		fields = append(fields, requiredFieldReleaseType)
	}

	if matches["additional"] != "" {
		if version.Additional, err = util.AtoiUint64(matches["additional"]); err != nil {
			return version, fields, fmt.Errorf("can't parse Additional: %w", err)
		}
		fields = append(fields, requiredFieldAdditional)
	}

	version.Hash = matches["hash"]
	if version.Hash != "" {
		fields = append(fields, requiredFieldHash)
	}

	if matches["revision"] != "" {
		if version.Revision, err = util.AtoiUint64(matches["revision"]); err != nil {
			return version, fields, fmt.Errorf("can't parse Revision: %w", err)
		}
		fields = append(fields, requiredFieldRevision)
	}

	version.Str = verStr

	return version, fields, nil
}

// compareVersions compares with two Version according list of fields.
//
// Return `true` if all required version components the same.
func compareVersions(ref, other Version, fields requiredFields) bool {
	for _, m := range fields {
		isMatch := false
		switch m {
		case requiredFieldBuildName:
			isMatch = ref.BuildName == other.BuildName
		case requiredFieldMajor:
			isMatch = ref.Major == other.Major
		case requiredFieldMinor:
			isMatch = ref.Minor == other.Minor
		case requiredFieldPatch:
			isMatch = ref.Patch == other.Patch
		case requiredFieldReleaseType:
			isMatch = ref.Release.Type == other.Release.Type
		case requiredFieldReleaseNum:
			isMatch = ref.Release.Num == other.Release.Num
		case requiredFieldAdditional:
			isMatch = ref.Additional == other.Additional
		case requiredFieldHash:
			isMatch = ref.Hash == other.Hash
		case requiredFieldRevision:
			isMatch = ref.Revision == other.Revision
		}
		if !isMatch {
			return false
		}
	}
	return true
}

// IsVersion checks is string looks like version.
// If isStrict is true, then version <major.minor.patch> components are required.
func IsVersion(version string, isStrict bool) bool {
	re := createVersionRegexp(isStrict)
	return re.MatchString(version)
}

// MatchVersion takes as input an ascending sorted list of versions.
// In which it finds the latest version matching the search mask.
//
// Return string of found version or `NotFoundVersion` with error if just not found.
func MatchVersion(expected string, sortedVersions []Version) (string, error) {
	reference, fields, err := exploreMatchVersion(expected)
	if err != nil {
		return "", err
	}
	for i := len(sortedVersions) - 1; i >= 0; i-- {
		ver := sortedVersions[i]
		if compareVersions(reference, ver, fields) {
			return ver.Str, nil
		}
	}
	return "", NotFoundError{expected}
}
