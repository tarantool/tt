package version

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/tarantool/tt/v3/cli/util"
)

var numericVersionMask = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,2}$`)

var (
	// ErrNotFound reports that no available version matches the requested version.
	ErrNotFound = errors.New("version not found")

	// ErrInstalledVersionNotFound reports a request matching no installed version.
	ErrInstalledVersionNotFound = errors.New("installed version not found")
	// ErrAmbiguousInstalledVersion reports a request matching multiple installed versions.
	ErrAmbiguousInstalledVersion = errors.New("more than one installed version matches")

	errMinorVersionRequiresMajorToBeSpecified = errors.New(
		"minor version requires major to be specified",
	)
	errPatchVersionRequiresMinorToBeSpecified = errors.New(
		"patch version requires minor to be specified",
	)
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

// NotFoundError reports a version request that could not be matched.
type NotFoundError struct {
	expected string
}

// Error implements the [error] interface.
func (e NotFoundError) Error() string {
	return fmt.Sprintf("not matched with: %q", e.expected)
}

// Unwrap allows errors.Is to recognize ErrNotFound while retaining the requested version.
func (e NotFoundError) Unwrap() error {
	return ErrNotFound
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

	version.BuildName = matches["buildName"]
	if version.BuildName != "" {
		fields = append(fields, requiredFieldBuildName)
	}

	if matches["major"] != "" {
		version.Major, err = util.AtoiUint64(matches["major"])
		if err != nil {
			return version, fields, fmt.Errorf("can't parse Major: %w", err)
		}

		fields = append(fields, requiredFieldMajor)
	}

	if matches["minor"] != "" {
		if matches["major"] == "" {
			return version, fields, errMinorVersionRequiresMajorToBeSpecified
		}

		version.Minor, err = util.AtoiUint64(matches["minor"])
		if err != nil {
			return version, fields, fmt.Errorf("can't parse Minor: %w", err)
		}

		fields = append(fields, requiredFieldMinor)
	}

	if matches["patch"] != "" {
		if matches["minor"] == "" {
			return version, fields, errPatchVersionRequiresMinorToBeSpecified
		}

		version.Patch, err = util.AtoiUint64(matches["patch"])
		if err != nil {
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
		version.Additional, err = util.AtoiUint64(matches["additional"])
		if err != nil {
			return version, fields, fmt.Errorf("can't parse Additional: %w", err)
		}

		fields = append(fields, requiredFieldAdditional)
	}

	version.Hash = matches["hash"]
	if version.Hash != "" {
		fields = append(fields, requiredFieldHash)
	}

	if matches["revision"] != "" {
		version.Revision, err = util.AtoiUint64(matches["revision"])
		if err != nil {
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
		case countOfRequiredFields:
			// The sentinel is not a version field and never matches.
		}

		if !isMatch {
			return false
		}
	}

	return true
}

// IsVersion reports whether the string matches a version or a partial version mask.
func IsVersion(version string) bool {
	re := createVersionRegexp(false)
	return re.MatchString(version)
}

// IsNumericVersion reports whether the request has the form X, X.Y, or X.Y.Z.
func IsNumericVersion(requested string) bool {
	return numericVersionMask.MatchString(requested)
}

// MatchInstalledVersion resolves X.Y.Z or an exact installation name.
// A numeric request must match exactly one build, including prereleases and revisions.
// Partial numbers are never expanded. Literal names such as commit hashes match exactly.
func MatchInstalledVersion(requested string, installed []string) (string, error) {
	if requested == "" {
		return "", fmt.Errorf("%w: %q", ErrInstalledVersionNotFound, requested)
	}

	reference, err := Parse(requested)
	numeric := err == nil && IsNumericVersion(requested)
	fields := requiredFields{requiredFieldMajor, requiredFieldMinor, requiredFieldPatch}

	var matches []string

	for _, candidate := range installed {
		if candidate != requested {
			if !numeric {
				continue
			}

			parsed, err := Parse(candidate)
			if err != nil || !compareVersions(reference, parsed, fields) {
				continue
			}
		}

		if !slices.Contains(matches, candidate) {
			matches = append(matches, candidate)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%w: %q", ErrInstalledVersionNotFound, requested)
	case 1:
		return matches[0], nil
	default:
		slices.Sort(matches)

		return "", fmt.Errorf("%w %q: %s; specify the full version name",
			ErrAmbiguousInstalledVersion, requested, strings.Join(matches, ", "))
	}
}

// MatchVersion takes as input an ascending sorted list of versions.
// In which it finds the latest version matching the search mask.
//
// It returns NotFoundError wrapping ErrNotFound when no version matches.
func MatchVersion(expected string, sortedVersions []Version) (string, error) {
	reference, fields, err := exploreMatchVersion(expected)
	if err != nil {
		return "", err
	}

	for _, ver := range slices.Backward(sortedVersions) {
		if compareVersions(reference, ver, fields) {
			return ver.Str, nil
		}
	}

	return "", NotFoundError{expected}
}
